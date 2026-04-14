package oidc

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"fmt"
	"sync"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	"github.com/aws/aws-sdk-go-v2/service/kms/types"
)

// AWSKMSClient is the subset of the AWS KMS API that the signer needs.
// Exposed as an interface so tests can pass a fake client.
type AWSKMSClient interface {
	Sign(ctx context.Context, params *kms.SignInput, optFns ...func(*kms.Options)) (*kms.SignOutput, error)
	GetPublicKey(ctx context.Context, params *kms.GetPublicKeyInput, optFns ...func(*kms.Options)) (*kms.GetPublicKeyOutput, error)
}

// AWSKMSSigner signs JWTs by delegating to an AWS KMS asymmetric key.
// The private key never leaves KMS.
type AWSKMSSigner struct {
	client AWSKMSClient
	keyID  string

	once   sync.Once
	pubKey *rsa.PublicKey
	kid    string
	err    error
}

// NewAWSKMSSigner constructs a signer bound to the given KMS key. The
// keyID may be the key ARN, alias ARN, or alias name — anything
// accepted by kms:Sign and kms:GetPublicKey.
func NewAWSKMSSigner(ctx context.Context, keyID string) (*AWSKMSSigner, error) {
	if keyID == "" {
		return nil, fmt.Errorf("oidc: empty AWS KMS keyID")
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("oidc: load aws config: %w", err)
	}
	return NewAWSKMSSignerFromClient(kms.NewFromConfig(cfg), keyID), nil
}

// NewAWSKMSSignerFromClient lets callers (mostly tests) inject a fake
// KMS client. The public key is still fetched lazily via the client.
func NewAWSKMSSignerFromClient(client AWSKMSClient, keyID string) *AWSKMSSigner {
	return &AWSKMSSigner{client: client, keyID: keyID}
}

// Sign calls kms:Sign with the raw SHA-256 digest and the signing
// algorithm RSASSA_PKCS1_V1_5_SHA_256, which matches what RS256 JWS
// signatures expect.
func (s *AWSKMSSigner) Sign(ctx context.Context, digest []byte) ([]byte, error) {
	out, err := s.client.Sign(ctx, &kms.SignInput{
		KeyId:            &s.keyID,
		Message:          digest,
		MessageType:      types.MessageTypeDigest,
		SigningAlgorithm: types.SigningAlgorithmSpecRsassaPkcs1V15Sha256,
	})
	if err != nil {
		return nil, fmt.Errorf("oidc: kms:Sign: %w", err)
	}
	return out.Signature, nil
}

// PublicKey fetches the public half of the KMS key once and caches it.
// Subsequent calls return the cached value.
func (s *AWSKMSSigner) PublicKey(ctx context.Context) (*rsa.PublicKey, error) {
	s.resolveOnce(ctx)
	return s.pubKey, s.err
}

// KeyID returns the JWK kid derived from the cached public key.
func (s *AWSKMSSigner) KeyID(ctx context.Context) (string, error) {
	s.resolveOnce(ctx)
	return s.kid, s.err
}

func (s *AWSKMSSigner) resolveOnce(ctx context.Context) {
	s.once.Do(func() {
		out, err := s.client.GetPublicKey(ctx, &kms.GetPublicKeyInput{KeyId: &s.keyID})
		if err != nil {
			s.err = fmt.Errorf("oidc: kms:GetPublicKey: %w", err)
			return
		}
		pub, err := x509.ParsePKIXPublicKey(out.PublicKey)
		if err != nil {
			s.err = fmt.Errorf("oidc: parse kms public key: %w", err)
			return
		}
		rsaPub, ok := pub.(*rsa.PublicKey)
		if !ok {
			s.err = fmt.Errorf("oidc: kms key is not RSA (got %T)", pub)
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
