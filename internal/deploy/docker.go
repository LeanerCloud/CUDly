package deploy

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

// validDockerRef matches safe Docker image reference components: must start
// with an alphanumeric character (no leading hyphen -- leading hyphens would
// be interpreted as flags by docker) and may then contain alphanumerics plus
// the characters that appear in valid registry hosts, repository paths, tags,
// and digest prefixes (dots, colons, slashes, at-signs, hyphens).
var validDockerRef = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/:@-]*$`)

// validateDockerRef returns an error if ref is empty or contains characters
// outside the safe set (including a leading hyphen).
func validateDockerRef(label, ref string) error {
	if ref == "" {
		return fmt.Errorf("docker %s must not be empty", label)
	}
	if !validDockerRef.MatchString(ref) {
		return fmt.Errorf("docker %s contains invalid characters: %q", label, ref)
	}
	return nil
}

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
	if err := validateDockerRef("tag", tag); err != nil {
		return err
	}
	if err := validateDockerRef("image name", imageName); err != nil {
		return err
	}
	platform := fmt.Sprintf("linux/%s", architecture)
	return s.CmdRunner.Run("docker", "build",
		"--platform", platform,
		"-t", fmt.Sprintf("%s:%s", imageName, tag),
		".")
}

// TagImage tags a Docker image with a new name.
func (s *DockerService) TagImage(sourceTag, targetTag string) error {
	if err := validateDockerRef("source tag", sourceTag); err != nil {
		return err
	}
	if err := validateDockerRef("target tag", targetTag); err != nil {
		return err
	}
	return s.CmdRunner.Run("docker", "tag", sourceTag, targetTag)
}

// PushImage pushes a Docker image to a registry.
func (s *DockerService) PushImage(imageTag string) error {
	if err := validateDockerRef("image tag", imageTag); err != nil {
		return err
	}
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
	cmd := exec.Command(name, args...) // #nosec G204 -- deploy tooling: callers hardcode binary names (npm, docker, aws); no user input reaches this function
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// RunWithStdin runs a command with stdin input and streams output to stdout/stderr.
func (r *DefaultCommandRunner) RunWithStdin(name string, stdin string, args ...string) error {
	cmd := exec.Command(name, args...) // #nosec G204 -- deploy tooling: callers hardcode binary names (npm, docker, aws); no user input reaches this function
	cmd.Stdin = strings.NewReader(stdin)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
