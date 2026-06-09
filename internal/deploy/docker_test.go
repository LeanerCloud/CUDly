package deploy

import (
	"errors"
	"strings"
	"testing"
)

func TestDockerService_BuildImage(t *testing.T) {
	mockRunner := &MockCommandRunner{}
	service := NewDockerService(mockRunner)

	err := service.BuildImage("arm64", "latest", "myimage")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(mockRunner.Commands) != 1 {
		t.Fatalf("expected 1 command, got %d", len(mockRunner.Commands))
	}

	cmd := mockRunner.Commands[0]
	expected := []string{"docker", "build", "--platform", "linux/arm64", "-t", "myimage:latest", "."}

	if len(cmd) != len(expected) {
		t.Errorf("expected %v, got %v", expected, cmd)
	}
	for i, v := range expected {
		if cmd[i] != v {
			t.Errorf("expected %s at position %d, got %s", v, i, cmd[i])
		}
	}
}

func TestDockerService_BuildImage_x86(t *testing.T) {
	mockRunner := &MockCommandRunner{}
	service := NewDockerService(mockRunner)

	err := service.BuildImage("x86_64", "v1.0", "myimage")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cmd := mockRunner.Commands[0]
	// Check platform is linux/x86_64
	if cmd[3] != "linux/x86_64" {
		t.Errorf("expected platform linux/x86_64, got %s", cmd[3])
	}
	// Check tag
	if cmd[5] != "myimage:v1.0" {
		t.Errorf("expected tag myimage:v1.0, got %s", cmd[5])
	}
}

func TestDockerService_TagImage(t *testing.T) {
	mockRunner := &MockCommandRunner{}
	service := NewDockerService(mockRunner)

	err := service.TagImage("source:tag", "target:tag")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(mockRunner.Commands) != 1 {
		t.Fatalf("expected 1 command, got %d", len(mockRunner.Commands))
	}

	cmd := mockRunner.Commands[0]
	expected := []string{"docker", "tag", "source:tag", "target:tag"}

	for i, v := range expected {
		if cmd[i] != v {
			t.Errorf("expected %s at position %d, got %s", v, i, cmd[i])
		}
	}
}

func TestDockerService_PushImage(t *testing.T) {
	mockRunner := &MockCommandRunner{}
	service := NewDockerService(mockRunner)

	err := service.PushImage("myrepo:latest")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(mockRunner.Commands) != 1 {
		t.Fatalf("expected 1 command, got %d", len(mockRunner.Commands))
	}

	cmd := mockRunner.Commands[0]
	expected := []string{"docker", "push", "myrepo:latest"}

	for i, v := range expected {
		if cmd[i] != v {
			t.Errorf("expected %s at position %d, got %s", v, i, cmd[i])
		}
	}
}

func TestDockerService_PushToECR(t *testing.T) {
	mockRunner := &MockCommandRunner{}
	service := NewDockerService(mockRunner)

	err := service.PushToECR("local:latest", "123456789012.dkr.ecr.us-east-1.amazonaws.com/repo:latest")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have called tag and push
	if len(mockRunner.Commands) != 2 {
		t.Fatalf("expected 2 commands, got %d", len(mockRunner.Commands))
	}

	// First command should be tag
	if mockRunner.Commands[0][0] != "docker" || mockRunner.Commands[0][1] != "tag" {
		t.Errorf("expected docker tag, got %v", mockRunner.Commands[0])
	}

	// Second command should be push
	if mockRunner.Commands[1][0] != "docker" || mockRunner.Commands[1][1] != "push" {
		t.Errorf("expected docker push, got %v", mockRunner.Commands[1])
	}
}

func TestDockerService_PushToECR_TagError(t *testing.T) {
	mockRunner := &MockCommandRunner{
		RunFunc: func(name string, args ...string) error {
			if args[0] == "tag" {
				return errors.New("tag failed")
			}
			return nil
		},
	}
	service := NewDockerService(mockRunner)

	err := service.PushToECR("local:latest", "remote:latest")
	if err == nil {
		t.Error("expected error, got nil")
	}
	if err.Error() != "docker tag failed: tag failed" {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestDockerService_PushToECR_PushError(t *testing.T) {
	mockRunner := &MockCommandRunner{
		RunFunc: func(name string, args ...string) error {
			if args[0] == "push" {
				return errors.New("push failed")
			}
			return nil
		},
	}
	service := NewDockerService(mockRunner)

	err := service.PushToECR("local:latest", "remote:latest")
	if err == nil {
		t.Error("expected error, got nil")
	}
	if err.Error() != "docker push failed: push failed" {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestDefaultCommandRunner_Run_Success(t *testing.T) {
	runner := NewDefaultCommandRunner()

	// Use 'true' command which always succeeds
	err := runner.Run("true")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDefaultCommandRunner_Run_WithArgs(t *testing.T) {
	runner := NewDefaultCommandRunner()

	// Use 'echo' command with arguments
	err := runner.Run("echo", "hello", "world")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDefaultCommandRunner_Run_Failure(t *testing.T) {
	runner := NewDefaultCommandRunner()

	// Use 'false' command which always fails
	err := runner.Run("false")
	if err == nil {
		t.Error("expected error, got nil")
	}
}

func TestDefaultCommandRunner_RunWithStdin_Success(t *testing.T) {
	runner := NewDefaultCommandRunner()

	// Use 'cat' to echo back stdin
	err := runner.RunWithStdin("cat", "test input")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDefaultCommandRunner_RunWithStdin_Failure(t *testing.T) {
	runner := NewDefaultCommandRunner()

	// Use a command that will fail
	err := runner.RunWithStdin("false", "test input")
	if err == nil {
		t.Error("expected error, got nil")
	}
}

func TestNewDefaultCommandRunner(t *testing.T) {
	runner := NewDefaultCommandRunner()
	if runner == nil {
		t.Error("expected non-nil runner")
	}
}

// TestDockerService_TagValidation verifies that leading-dash and other unsafe
// values are rejected before being passed to exec.Command (07-N1).
func TestDockerService_TagValidation(t *testing.T) {
	cases := []struct {
		name        string
		fn          func(*DockerService) error
		wantErrFrag string
	}{
		{
			name:        "BuildImage leading-dash tag",
			fn:          func(s *DockerService) error { return s.BuildImage("arm64", "-v", "myimage") },
			wantErrFrag: "invalid characters",
		},
		{
			name:        "BuildImage leading-dash imageName",
			fn:          func(s *DockerService) error { return s.BuildImage("arm64", "latest", "-rm") },
			wantErrFrag: "invalid characters",
		},
		{
			name:        "BuildImage empty tag",
			fn:          func(s *DockerService) error { return s.BuildImage("arm64", "", "myimage") },
			wantErrFrag: "must not be empty",
		},
		{
			name:        "TagImage invalid source",
			fn:          func(s *DockerService) error { return s.TagImage("--no-trunc", "target:tag") },
			wantErrFrag: "invalid characters",
		},
		{
			name:        "TagImage invalid target",
			fn:          func(s *DockerService) error { return s.TagImage("source:tag", "--rm") },
			wantErrFrag: "invalid characters",
		},
		{
			name:        "PushImage invalid tag",
			fn:          func(s *DockerService) error { return s.PushImage("--all-tags") },
			wantErrFrag: "invalid characters",
		},
		{
			name:        "PushImage empty tag",
			fn:          func(s *DockerService) error { return s.PushImage("") },
			wantErrFrag: "must not be empty",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			service := NewDockerService(&MockCommandRunner{})
			err := tc.fn(service)
			if err == nil {
				t.Fatal("expected validation error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantErrFrag) {
				t.Errorf("expected error containing %q, got: %v", tc.wantErrFrag, err)
			}
		})
	}
}

// TestDockerService_TagValidation_ValidRefs confirms that legitimate image
// references (with dots, colons, slashes, hyphens) are accepted.
func TestDockerService_TagValidation_ValidRefs(t *testing.T) {
	cases := []struct {
		name string
		fn   func(*DockerService) error
	}{
		{
			name: "ECR-style tag",
			fn: func(s *DockerService) error {
				return s.PushImage("123456789012.dkr.ecr.us-east-1.amazonaws.com/myrepo:v1.2.3")
			},
		},
		{
			name: "simple tag with hyphen",
			fn:   func(s *DockerService) error { return s.PushImage("myimage:v1-rc1") },
		},
		{
			name: "digest reference",
			fn:   func(s *DockerService) error { return s.PushImage("myimage@sha256:abc123") },
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			service := NewDockerService(&MockCommandRunner{})
			if err := tc.fn(service); err != nil {
				t.Errorf("unexpected validation error for valid ref: %v", err)
			}
		})
	}
}
