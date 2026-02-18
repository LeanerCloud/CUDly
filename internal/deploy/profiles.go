package deploy

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"
)

// ProfileConfig holds configuration for a single deployment profile
type ProfileConfig struct {
	Provider          string  `yaml:"provider"`         // Cloud provider: aws, azure, gcp
	ComputePlatform   string  `yaml:"compute_platform"` // Compute platform: lambda/fargate, container-apps/aks, cloud-run/gke
	StackName         string  `yaml:"stack_name"`
	Region            string  `yaml:"region"`
	AWSProfile        string  `yaml:"aws_profile"`
	Email             string  `yaml:"email"`
	Term              int     `yaml:"term"`
	PaymentOption     string  `yaml:"payment_option"`
	Coverage          float64 `yaml:"coverage"`
	RampSchedule      string  `yaml:"ramp_schedule"`
	NotifyDays        int     `yaml:"notify_days"`
	EnableDashboard   bool    `yaml:"enable_dashboard"`
	DashboardDomain   string  `yaml:"dashboard_domain,omitempty"`
	HostedZoneID      string  `yaml:"hosted_zone_id,omitempty"`
	Architecture      string  `yaml:"architecture"`
	MemorySize        int     `yaml:"memory_size"`
	ImageTag          string  `yaml:"image_tag,omitempty"`
	CORSAllowedOrigin string  `yaml:"cors_allowed_origin,omitempty"`
	AdminEmail        string  `yaml:"admin_email,omitempty"`
}

// DeploymentConfig holds all deployment profiles
type DeploymentConfig struct {
	ActiveProfile string                   `yaml:"active_profile"`
	Profiles      map[string]ProfileConfig `yaml:"profiles"`
}

// GetConfigPath returns the path to the deployment configuration file
func GetConfigPath() string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(homeDir, ".cudly", "deployment.yaml")
}

// GetConfigDir returns the directory containing the deployment configuration
func GetConfigDir() string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(homeDir, ".cudly")
}

// LoadConfig loads the deployment configuration from disk
func LoadConfig() (*DeploymentConfig, error) {
	configPath := GetConfigPath()

	// If config doesn't exist, return empty config
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return &DeploymentConfig{
			Profiles: make(map[string]ProfileConfig),
		}, nil
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var config DeploymentConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	// Initialize profiles map if nil
	if config.Profiles == nil {
		config.Profiles = make(map[string]ProfileConfig)
	}

	return &config, nil
}

// SaveConfig saves the deployment configuration to disk
func SaveConfig(config *DeploymentConfig) error {
	configDir := GetConfigDir()
	configPath := GetConfigPath()

	// Ensure the config directory exists
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	data, err := yaml.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	if err := os.WriteFile(configPath, data, 0600); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	return nil
}

// InitConfig creates a default configuration file
func InitConfig() error {
	configPath := GetConfigPath()

	// Don't overwrite existing config
	if _, err := os.Stat(configPath); err == nil {
		return fmt.Errorf("configuration file already exists at %s", configPath)
	}

	// Create default profile
	defaultProfile := ProfileConfig{
		StackName:       "cudly",
		Region:          "us-east-1",
		Term:            3,
		PaymentOption:   "no-upfront",
		Coverage:        80,
		RampSchedule:    "immediate",
		NotifyDays:      3,
		EnableDashboard: true,
		Architecture:    "arm64",
		MemorySize:      512,
	}

	config := &DeploymentConfig{
		ActiveProfile: "default",
		Profiles: map[string]ProfileConfig{
			"default": defaultProfile,
		},
	}

	return SaveConfig(config)
}

// GetActiveProfile returns the active profile configuration
func (c *DeploymentConfig) GetActiveProfile() (*ProfileConfig, error) {
	if c.ActiveProfile == "" {
		return nil, fmt.Errorf("no active profile set")
	}

	profile, ok := c.Profiles[c.ActiveProfile]
	if !ok {
		return nil, fmt.Errorf("active profile %q not found", c.ActiveProfile)
	}

	return &profile, nil
}

// GetProfile returns a specific profile by name
func (c *DeploymentConfig) GetProfile(name string) (*ProfileConfig, error) {
	profile, ok := c.Profiles[name]
	if !ok {
		return nil, fmt.Errorf("profile %q not found", name)
	}
	return &profile, nil
}

// SetActiveProfile sets the active profile
func (c *DeploymentConfig) SetActiveProfile(name string) error {
	if _, ok := c.Profiles[name]; !ok {
		return fmt.Errorf("profile %q not found", name)
	}
	c.ActiveProfile = name
	return nil
}

// AddProfile adds a new profile to the configuration
func (c *DeploymentConfig) AddProfile(name string, profile ProfileConfig) error {
	if name == "" {
		return fmt.Errorf("profile name cannot be empty")
	}

	if _, exists := c.Profiles[name]; exists {
		return fmt.Errorf("profile %q already exists", name)
	}

	c.Profiles[name] = profile

	// If this is the first profile, make it active
	if len(c.Profiles) == 1 || c.ActiveProfile == "" {
		c.ActiveProfile = name
	}

	return nil
}

// UpdateProfile updates an existing profile
func (c *DeploymentConfig) UpdateProfile(name string, profile ProfileConfig) error {
	if _, exists := c.Profiles[name]; !exists {
		return fmt.Errorf("profile %q does not exist", name)
	}

	c.Profiles[name] = profile
	return nil
}

// DeleteProfile removes a profile from the configuration
func (c *DeploymentConfig) DeleteProfile(name string) error {
	if _, ok := c.Profiles[name]; !ok {
		return fmt.Errorf("profile %q not found", name)
	}

	// Don't allow deleting the active profile
	if c.ActiveProfile == name {
		return fmt.Errorf("cannot delete active profile; set a different active profile first")
	}

	delete(c.Profiles, name)
	return nil
}

// CopyProfile creates a new profile by copying an existing one
func (c *DeploymentConfig) CopyProfile(from, to string) error {
	if to == "" {
		return fmt.Errorf("new profile name cannot be empty")
	}

	if _, exists := c.Profiles[to]; exists {
		return fmt.Errorf("profile %q already exists", to)
	}

	sourceProfile, ok := c.Profiles[from]
	if !ok {
		return fmt.Errorf("source profile %q not found", from)
	}

	// Create a copy of the source profile
	c.Profiles[to] = sourceProfile
	return nil
}

// ListProfiles returns a sorted list of profile names
func (c *DeploymentConfig) ListProfiles() []string {
	names := make([]string, 0, len(c.Profiles))
	for name := range c.Profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// HasProfile checks if a profile exists
func (c *DeploymentConfig) HasProfile(name string) bool {
	_, ok := c.Profiles[name]
	return ok
}

// ProfileCount returns the number of profiles
func (c *DeploymentConfig) ProfileCount() int {
	return len(c.Profiles)
}
