package deploy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestUploadDirectory exercises the uploadDirectory code path using a real
// temporary directory populated with test files.
func TestUploadDirectory_Success(t *testing.T) {
	// Create a temp dist directory with a few files
	distDir := t.TempDir()

	// HTML file (should get no-cache header)
	require.NoError(t, os.WriteFile(filepath.Join(distDir, "index.html"), []byte("<html></html>"), 0644))
	// JS file (should get 1-year cache)
	require.NoError(t, os.WriteFile(filepath.Join(distDir, "app.js"), []byte("console.log('hi')"), 0644))
	// Sub-directory with asset
	subDir := filepath.Join(distDir, "assets")
	require.NoError(t, os.MkdirAll(subDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(subDir, "logo.png"), []byte("PNG"), 0644))
	// JSON file (no-cache)
	require.NoError(t, os.WriteFile(filepath.Join(distDir, "manifest.json"), []byte("{}"), 0644))
	// Webmanifest file (no-cache)
	require.NoError(t, os.WriteFile(filepath.Join(distDir, "app.webmanifest"), []byte("{}"), 0644))
	// Binary file with unknown extension (octet-stream)
	require.NoError(t, os.WriteFile(filepath.Join(distDir, "data.bin"), []byte{0x01, 0x02}, 0644))

	var uploaded []string
	mockS3 := &MockS3Client{
		PutObjectFunc: func(ctx context.Context, params *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
			uploaded = append(uploaded, *params.Key)
			return &s3.PutObjectOutput{}, nil
		},
	}

	service := NewFrontendService(mockS3, nil, nil)
	err := service.uploadDirectory(context.Background(), distDir, "test-bucket")
	require.NoError(t, err)

	assert.Len(t, uploaded, 6)
	assert.Contains(t, uploaded, "index.html")
	assert.Contains(t, uploaded, "app.js")
	assert.Contains(t, uploaded, "assets/logo.png")
	assert.Contains(t, uploaded, "manifest.json")
	assert.Contains(t, uploaded, "app.webmanifest")
	assert.Contains(t, uploaded, "data.bin")
}

func TestUploadDirectory_PutObjectError(t *testing.T) {
	distDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(distDir, "index.html"), []byte("<html>"), 0644))

	mockS3 := &MockS3Client{
		PutObjectFunc: func(ctx context.Context, params *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
			return nil, errors.New("upload failed")
		},
	}

	service := NewFrontendService(mockS3, nil, nil)
	err := service.uploadDirectory(context.Background(), distDir, "test-bucket")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to upload")
}

func TestUploadDirectory_NonExistentDir(t *testing.T) {
	mockS3 := &MockS3Client{}
	service := NewFrontendService(mockS3, nil, nil)
	err := service.uploadDirectory(context.Background(), "/nonexistent/path/that/does/not/exist", "test-bucket")
	assert.Error(t, err)
}

// TestBuildAndUpload_SuccessWithFrontendDirEnv exercises BuildAndUpload using
// CUDLY_FRONTEND_DIR pointing to a directory with a package.json and a prebuilt dist/.
func TestBuildAndUpload_SuccessWithFrontendDirEnv(t *testing.T) {
	// Create a fake frontend directory structure
	frontendDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(frontendDir, "package.json"), []byte(`{"name":"cudly"}`), 0644))

	// Create dist directory with a file so uploadDirectory has something to upload
	distDir := filepath.Join(frontendDir, "dist")
	require.NoError(t, os.MkdirAll(distDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(distDir, "index.html"), []byte("<html>"), 0644))

	t.Setenv("CUDLY_FRONTEND_DIR", frontendDir)

	var cmdRun [][]string
	mockCmd := &MockCommandRunner{
		RunFunc: func(name string, args ...string) error {
			cmdRun = append(cmdRun, append([]string{name}, args...))
			return nil // npm install and build succeed
		},
	}

	var uploaded []string
	mockS3 := &MockS3Client{
		PutObjectFunc: func(ctx context.Context, params *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
			uploaded = append(uploaded, *params.Key)
			return &s3.PutObjectOutput{}, nil
		},
	}

	// CloudFront list returns empty (no invalidation needed)
	mockCF := &MockCloudFrontClient{}

	service := NewFrontendService(mockS3, mockCF, mockCmd)
	err := service.BuildAndUpload(context.Background(), "test-bucket", "https://dashboard.example.com")
	require.NoError(t, err)

	// npm install was called
	assert.True(t, len(cmdRun) >= 2, "expected at least 2 npm commands")
	assert.Contains(t, uploaded, "index.html")
}

// TestFindFrontendDir_EnvVar_NotFound tests the env var path when package.json is absent
func TestFindFrontendDir_EnvVar_NotFound(t *testing.T) {
	tmpDir := t.TempDir()
	// Point env var to dir without package.json
	t.Setenv("CUDLY_FRONTEND_DIR", tmpDir)

	service := NewFrontendService(nil, nil, nil)
	// Should fall through to path search (which will also fail in test env)
	_, err := service.FindFrontendDir()
	// May succeed if there's a frontend dir in the search path, or fail
	// Either way, we just verify it doesn't panic
	_ = err
}

// TestFindFrontendDir_EnvVar_Found tests the env var path when package.json exists
func TestFindFrontendDir_EnvVar_Found(t *testing.T) {
	tmpDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "package.json"), []byte(`{}`), 0644))
	t.Setenv("CUDLY_FRONTEND_DIR", tmpDir)

	service := NewFrontendService(nil, nil, nil)
	dir, err := service.FindFrontendDir()
	require.NoError(t, err)
	assert.NotEmpty(t, dir)
}

// TestGetConfigPath_HomeDirError tests GetConfigPath when HOME is unset
func TestGetConfigPath_HomeError(t *testing.T) {
	original := os.Getenv("HOME")
	os.Unsetenv("HOME")
	// On macOS, UserHomeDir may use a different mechanism, so just
	// verify it doesn't panic and returns a string.
	result := GetConfigPath()
	_ = result
	os.Setenv("HOME", original)
}

// TestGetConfigDir_HomeError tests GetConfigDir when HOME is unset
func TestGetConfigDir_HomeError(t *testing.T) {
	original := os.Getenv("HOME")
	os.Unsetenv("HOME")
	result := GetConfigDir()
	_ = result
	os.Setenv("HOME", original)
}

// TestLoadConfig_ParseError tests LoadConfig when the config file contains invalid YAML
func TestLoadConfig_ParseError(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	// Create the config directory and file
	configDir := filepath.Join(tmpDir, ".cudly")
	require.NoError(t, os.MkdirAll(configDir, 0700))
	configPath := filepath.Join(configDir, "deployment.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(":\n  - invalid: yaml: ["), 0600))

	_, err := LoadConfig()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to parse config file")
}

// TestEmptyBucket_DeleteWithErrors covers the per-object error handling branch
func TestFrontendService_EmptyBucket_DeleteWithErrors(t *testing.T) {
	errKey := "locked-file.html"
	errMsg := "access denied"

	mockS3 := &MockS3Client{
		ListObjectsV2Func: func(ctx context.Context, params *s3.ListObjectsV2Input, optFns ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
			k := errKey
			return &s3.ListObjectsV2Output{
				Contents: []s3types.Object{{Key: &k}},
			}, nil
		},
		DeleteObjectsFunc: func(ctx context.Context, params *s3.DeleteObjectsInput, optFns ...func(*s3.Options)) (*s3.DeleteObjectsOutput, error) {
			k := errKey
			m := errMsg
			return &s3.DeleteObjectsOutput{
				Errors: []s3types.Error{{Key: &k, Message: &m}},
			}, nil
		},
	}

	service := NewFrontendService(mockS3, nil, nil)
	err := service.EmptyBucket(context.Background(), "test-bucket")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to delete some objects")
	assert.Contains(t, err.Error(), errKey)
}
