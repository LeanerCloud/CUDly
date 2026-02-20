package deploy

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
)

// DockerService handles Docker operations.
type DockerService struct {
	CmdRunner CommandRunner
}

// NewDockerService creates a new DockerService.
func NewDockerService(cmdRunner CommandRunner) *DockerService {
	return &DockerService{
		CmdRunner: cmdRunner,
	}
}

// BuildImage builds a Docker image with the specified architecture and tag.
func (s *DockerService) BuildImage(architecture, tag, imageName string) error {
	platform := fmt.Sprintf("linux/%s", architecture)
	return s.CmdRunner.Run("docker", "build",
		"--platform", platform,
		"-t", fmt.Sprintf("%s:%s", imageName, tag),
		".")
}

// TagImage tags a Docker image with a new name.
func (s *DockerService) TagImage(sourceTag, targetTag string) error {
	return s.CmdRunner.Run("docker", "tag", sourceTag, targetTag)
}

// PushImage pushes a Docker image to a registry.
func (s *DockerService) PushImage(imageTag string) error {
	return s.CmdRunner.Run("docker", "push", imageTag)
}

// PushToECR tags and pushes an image to ECR.
func (s *DockerService) PushToECR(localImage, remoteTag string) error {
	if err := s.TagImage(localImage, remoteTag); err != nil {
		return fmt.Errorf("docker tag failed: %w", err)
	}

	if err := s.PushImage(remoteTag); err != nil {
		return fmt.Errorf("docker push failed: %w", err)
	}

	log.Printf("Image pushed: %s", remoteTag)
	return nil
}

// DefaultCommandRunner is the default implementation of CommandRunner.
type DefaultCommandRunner struct{}

// NewDefaultCommandRunner creates a new DefaultCommandRunner.
func NewDefaultCommandRunner() *DefaultCommandRunner {
	return &DefaultCommandRunner{}
}

// Run runs a command and streams output to stdout/stderr.
func (r *DefaultCommandRunner) Run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// RunWithStdin runs a command with stdin input and streams output to stdout/stderr.
func (r *DefaultCommandRunner) RunWithStdin(name string, stdin string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdin = strings.NewReader(stdin)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
