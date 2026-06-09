package oidc

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"hash/crc32"
	"sync"

	kms "cloud.google.com/go/kms/apiv1"
	kmspb "cloud.google.com/go/kms/apiv1/kmspb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// GCPKMSClient is the subset of the GCP Cloud KMS API the signer needs.
type GCPKMSClient interface {
	AsymmetricSign(ctx context.Context, req *kmspb.AsymmetricSignRequest, opts ...interface{}) (*kmspb.AsymmetricSignResponse, error)
	GetPublicKey(ctx context.Context, req *kmspb.GetPublicKeyRequest, opts ...interface{}) (*kmspb.PublicKey, error)
}

// gcpKMSWrapper adapts *kms.KeyManagementClient to the GCPKMSClient
// interface so real clients satisfy the same shape as the fakes used in
// tests.
type gcpKMSWrapper struct {
	real *kms.KeyManagementClient
}

func (w gcpKMSWrapper) AsymmetricSign(ctx context.Context, req *kmspb.AsymmetricSignRequest, _ ...interface{}) (*kmspb.AsymmetricSignResponse, error) {
	return w.real.AsymmetricSign(ctx, req)
}

func (w gcpKMSWrapper) GetPublicKey(ctx context.Context, req *kmspb.GetPublicKeyRequest, _ ...interface{}) (*kmspb.PublicKey, error) {
	return w.real.GetPublicKey(ctx, req)
}

// GCPKMSSigner signs JWTs using a GCP Cloud KMS asymmetric key. The
// private half never leaves the KMS.
type GCPKMSSigner struct {
	client      GCPKMSClient
	keyResource string // full resource name, incl. /cryptoKeyVersions/N

	once   sync.Once
	pubKey *rsa.PublicKey
	kid    string
	err    error
}

// NewGCPKMSSigner constructs a signer bound to a specific KMS key
// version resource. Example resource:
//
//	projects/.../locations/global/keyRings/.../cryptoKeys/.../cryptoKeyVersions/1
func NewGCPKMSSigner(ctx context.Context, keyResource string) (*GCPKMSSigner, error) {
	if keyResource == "" {
		return nil, fmt.Errorf("oidc: empty GCP KMS key resource")
	}
	client, err := kms.NewKeyManagementClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("oidc: gcp kms client: %w", err)
	}
	return NewGCPKMSSignerFromClient(gcpKMSWrapper{real: client}, keyResource), nil
}

// NewGCPKMSSignerFromClient lets callers inject a fake client (tests).
func NewGCPKMSSignerFromClient(client GCPKMSClient, keyResource string) *GCPKMSSigner {
	return &GCPKMSSigner{client: client, keyResource: keyResource}
}

// Sign calls AsymmetricSign with the SHA-256 digest. The caller must
// have already hashed the signing input; the digest is forwarded
// as-is with the expected algorithm
// RSA_SIGN_PKCS1_2048_SHA256 (implicit in the key config).
func (s *GCPKMSSigner) Sign(ctx context.Context, digest []byte) ([]byte, error) {
	crc := int64(crc32.Checksum(digest, crc32.MakeTable(crc32.Castagnoli)))
	req := &kmspb.AsymmetricSignRequest{
		Name: s.keyResource,
		Digest: &kmspb.Digest{
			Digest: &kmspb.Digest_Sha256{Sha256: digest},
		},
		DigestCrc32C: wrapperspb.Int64(crc),
	}
	resp, err := s.client.AsymmetricSign(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("oidc: gcp kms AsymmetricSign: %w", err)
	}
	return resp.Signature, nil
}

// PublicKey fetches the public half of the KMS key once and caches it.
func (s *GCPKMSSigner) PublicKey(ctx context.Context) (*rsa.PublicKey, error) {
	s.resolveOnce(ctx)
	return s.pubKey, s.err
}

// KeyID returns a stable kid derived from the cached public key.
func (s *GCPKMSSigner) KeyID(ctx context.Context) (string, error) {
	s.resolveOnce(ctx)
	return s.kid, s.err
}

func (s *GCPKMSSigner) resolveOnce(ctx context.Context) {
	s.once.Do(func() {
		resp, err := s.client.GetPublicKey(ctx, &kmspb.GetPublicKeyRequest{Name: s.keyResource})
		if err != nil {
			s.err = fmt.Errorf("oidc: gcp kms GetPublicKey: %w", err)
			return
		}
		block, _ := pem.Decode([]byte(resp.Pem))
		if block == nil {
			s.err = fmt.Errorf("oidc: gcp kms public key is not PEM-encoded")
			return
		}
		pub, err := x509.ParsePKIXPublicKey(block.Bytes)
		if err != nil {
			s.err = fmt.Errorf("oidc: parse gcp kms public key: %w", err)
			return
		}
		rsaPub, ok := pub.(*rsa.PublicKey)
		if !ok {
			s.err = fmt.Errorf("oidc: gcp kms key is not RSA (got %T)", pub)
			return
		}
		kid, err := ComputeKeyID(rsaPub)
		if err != nil {
			s.err = err
			return
		}
		s.pubKey = rsaPub
		s.kid = kid
	})
}
