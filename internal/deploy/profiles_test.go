package deploy

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProfileConfig(t *testing.T) {
	// Create a temporary directory for testing
	tmpDir, err := os.MkdirTemp("", "cudly-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	// Override the config path for testing
	originalHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", originalHome)

	t.Run("InitConfig creates default configuration", func(t *testing.T) {
		err := InitConfig()
		require.NoError(t, err)

		// Verify the config file was created
		configPath := GetConfigPath()
		assert.FileExists(t, configPath)

		// Load and verify the config
		config, err := LoadConfig()
		require.NoError(t, err)
		assert.Equal(t, "default", config.ActiveProfile)
		assert.Equal(t, 1, len(config.Profiles))
		assert.Contains(t, config.Profiles, "default")

		defaultProfile := config.Profiles["default"]
		assert.Equal(t, "cudly", defaultProfile.StackName)
		assert.Equal(t, "us-east-1", defaultProfile.Region)
		assert.Equal(t, 3, defaultProfile.Term)
		assert.Equal(t, "no-upfront", defaultProfile.PaymentOption)
		assert.Equal(t, 80.0, defaultProfile.Coverage)
		assert.Equal(t, "immediate", defaultProfile.RampSchedule)
		assert.Equal(t, 3, defaultProfile.NotifyDays)
		assert.True(t, defaultProfile.EnableDashboard)
		assert.Equal(t, "arm64", defaultProfile.Architecture)
		assert.Equal(t, 512, defaultProfile.MemorySize)
	})

	t.Run("InitConfig fails if config already exists", func(t *testing.T) {
		err := InitConfig()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "already exists")
	})

	t.Run("LoadConfig returns empty config when file doesn't exist", func(t *testing.T) {
		// Remove the config file
		os.Remove(GetConfigPath())

		config, err := LoadConfig()
		require.NoError(t, err)
		assert.NotNil(t, config)
		assert.Equal(t, 0, len(config.Profiles))
	})

	t.Run("AddProfile adds a new profile", func(t *testing.T) {
		config, err := LoadConfig()
		require.NoError(t, err)

		newProfile := ProfileConfig{
			StackName:       "cudly-prod",
			Region:          "us-west-2",
			Email:           "admin@example.com",
			Term:            1,
			PaymentOption:   "all-upfront",
			Coverage:        90,
			RampSchedule:    "gradual",
			NotifyDays:      7,
			EnableDashboard: true,
			Architecture:    "x86_64",
			MemorySize:      1024,
		}

		err = config.AddProfile("production", newProfile)
		require.NoError(t, err)
		assert.Equal(t, 1, len(config.Profiles))
		assert.Contains(t, config.Profiles, "production")

		// Save and reload to verify persistence
		err = SaveConfig(config)
		require.NoError(t, err)

		reloaded, err := LoadConfig()
		require.NoError(t, err)
		assert.Equal(t, 1, len(reloaded.Profiles))
		assert.Contains(t, reloaded.Profiles, "production")
		assert.Equal(t, "cudly-prod", reloaded.Profiles["production"].StackName)
	})

	t.Run("AddProfile fails for duplicate profile", func(t *testing.T) {
		config, err := LoadConfig()
		require.NoError(t, err)

		profile := ProfileConfig{StackName: "test"}
		err = config.AddProfile("production", profile)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "already exists")
	})

	t.Run("AddProfile fails for empty name", func(t *testing.T) {
		config, err := LoadConfig()
		require.NoError(t, err)

		profile := ProfileConfig{StackName: "test"}
		err = config.AddProfile("", profile)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "cannot be empty")
	})

	t.Run("SetActiveProfile changes active profile", func(t *testing.T) {
		config, err := LoadConfig()
		require.NoError(t, err)

		err = config.SetActiveProfile("production")
		require.NoError(t, err)
		assert.Equal(t, "production", config.ActiveProfile)

		// Save and verify
		err = SaveConfig(config)
		require.NoError(t, err)

		reloaded, err := LoadConfig()
		require.NoError(t, err)
		assert.Equal(t, "production", reloaded.ActiveProfile)
	})

	t.Run("SetActiveProfile fails for non-existent profile", func(t *testing.T) {
		config, err := LoadConfig()
		require.NoError(t, err)

		err = config.SetActiveProfile("nonexistent")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("GetActiveProfile returns active profile", func(t *testing.T) {
		config, err := LoadConfig()
		require.NoError(t, err)

		profile, err := config.GetActiveProfile()
		require.NoError(t, err)
		assert.NotNil(t, profile)
		assert.Equal(t, "cudly-prod", profile.StackName)
	})

	t.Run("GetProfile returns specific profile", func(t *testing.T) {
		config, err := LoadConfig()
		require.NoError(t, err)

		profile, err := config.GetProfile("production")
		require.NoError(t, err)
		assert.NotNil(t, profile)
		assert.Equal(t, "cudly-prod", profile.StackName)
	})

	t.Run("GetProfile fails for non-existent profile", func(t *testing.T) {
		config, err := LoadConfig()
		require.NoError(t, err)

		_, err = config.GetProfile("nonexistent")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("CopyProfile creates a copy", func(t *testing.T) {
		config, err := LoadConfig()
		require.NoError(t, err)

		err = config.CopyProfile("production", "staging")
		require.NoError(t, err)
		assert.Equal(t, 2, len(config.Profiles))
		assert.Contains(t, config.Profiles, "staging")

		// Verify the copy has the same settings
		prodProfile := config.Profiles["production"]
		stagingProfile := config.Profiles["staging"]
		assert.Equal(t, prodProfile.StackName, stagingProfile.StackName)
		assert.Equal(t, prodProfile.Region, stagingProfile.Region)
		assert.Equal(t, prodProfile.Term, stagingProfile.Term)

		// Save for next test
		err = SaveConfig(config)
		require.NoError(t, err)
	})

	t.Run("CopyProfile fails for non-existent source", func(t *testing.T) {
		config, err := LoadConfig()
		require.NoError(t, err)

		err = config.CopyProfile("nonexistent", "new")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("CopyProfile fails for existing destination", func(t *testing.T) {
		config, err := LoadConfig()
		require.NoError(t, err)

		// staging already exists from the previous test
		err = config.CopyProfile("production", "staging")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "already exists")
	})

	t.Run("CopyProfile fails for empty destination name", func(t *testing.T) {
		config, err := LoadConfig()
		require.NoError(t, err)

		err = config.CopyProfile("production", "")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "cannot be empty")
	})

	t.Run("DeleteProfile removes a profile", func(t *testing.T) {
		config, err := LoadConfig()
		require.NoError(t, err)

		// Should have production and staging at this point
		assert.Equal(t, 2, len(config.Profiles))

		err = config.DeleteProfile("staging")
		require.NoError(t, err)
		assert.Equal(t, 1, len(config.Profiles))
		assert.NotContains(t, config.Profiles, "staging")

		// Save for next tests
		err = SaveConfig(config)
		require.NoError(t, err)
	})

	t.Run("DeleteProfile fails for active profile", func(t *testing.T) {
		config, err := LoadConfig()
		require.NoError(t, err)

		// production is the active profile
		err = config.DeleteProfile("production")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "cannot delete active profile")
	})

	t.Run("DeleteProfile fails for non-existent profile", func(t *testing.T) {
		config, err := LoadConfig()
		require.NoError(t, err)

		err = config.DeleteProfile("nonexistent")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("ListProfiles returns sorted profile names", func(t *testing.T) {
		config, err := LoadConfig()
		require.NoError(t, err)

		// Add a few more profiles
		config.AddProfile("dev", ProfileConfig{StackName: "cudly-dev"})
		config.AddProfile("test", ProfileConfig{StackName: "cudly-test"})

		// Save to persist
		err = SaveConfig(config)
		require.NoError(t, err)

		// Reload and check
		config, err = LoadConfig()
		require.NoError(t, err)

		profiles := config.ListProfiles()
		assert.Equal(t, 3, len(profiles))
		// Should be sorted alphabetically
		assert.Equal(t, []string{"dev", "production", "test"}, profiles)
	})

	t.Run("HasProfile checks profile existence", func(t *testing.T) {
		config, err := LoadConfig()
		require.NoError(t, err)

		assert.True(t, config.HasProfile("production"))
		assert.True(t, config.HasProfile("dev"))
		assert.True(t, config.HasProfile("test"))
		assert.False(t, config.HasProfile("nonexistent"))
	})

	t.Run("ProfileCount returns correct count", func(t *testing.T) {
		config, err := LoadConfig()
		require.NoError(t, err)

		assert.Equal(t, 3, config.ProfileCount())
	})

	t.Run("UpdateProfile modifies existing profile", func(t *testing.T) {
		config, err := LoadConfig()
		require.NoError(t, err)

		updatedProfile := config.Profiles["dev"]
		updatedProfile.Region = "eu-west-1"
		updatedProfile.MemorySize = 2048

		err = config.UpdateProfile("dev", updatedProfile)
		require.NoError(t, err)

		profile, err := config.GetProfile("dev")
		require.NoError(t, err)
		assert.Equal(t, "eu-west-1", profile.Region)
		assert.Equal(t, 2048, profile.MemorySize)
	})

	t.Run("UpdateProfile fails for non-existent profile", func(t *testing.T) {
		config, err := LoadConfig()
		require.NoError(t, err)

		err = config.UpdateProfile("nonexistent", ProfileConfig{})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "does not exist")
	})

	t.Run("First profile becomes active automatically", func(t *testing.T) {
		config := &DeploymentConfig{
			Profiles: make(map[string]ProfileConfig),
		}

		profile := ProfileConfig{StackName: "first"}
		err := config.AddProfile("first", profile)
		require.NoError(t, err)

		assert.Equal(t, "first", config.ActiveProfile)
	})
}

func TestGetConfigPath(t *testing.T) {
	// Save original HOME
	originalHome := os.Getenv("HOME")
	defer os.Setenv("HOME", originalHome)

	// Set a test HOME
	testHome := "/tmp/test-home"
	os.Setenv("HOME", testHome)

	expected := filepath.Join(testHome, ".cudly", "deployment.yaml")
	actual := GetConfigPath()

	assert.Equal(t, expected, actual)
}

func TestGetConfigDir(t *testing.T) {
	// Save original HOME
	originalHome := os.Getenv("HOME")
	defer os.Setenv("HOME", originalHome)

	// Set a test HOME
	testHome := "/tmp/test-home"
	os.Setenv("HOME", testHome)

	expected := filepath.Join(testHome, ".cudly")
	actual := GetConfigDir()

	assert.Equal(t, expected, actual)
}

func TestGetActiveProfile_NoActiveProfile(t *testing.T) {
	config := &DeploymentConfig{
		ActiveProfile: "",
		Profiles: map[string]ProfileConfig{
			"test": {StackName: "test-stack"},
		},
	}

	_, err := config.GetActiveProfile()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no active profile set")
}

func TestGetActiveProfile_ActiveProfileNotFound(t *testing.T) {
	config := &DeploymentConfig{
		ActiveProfile: "nonexistent",
		Profiles: map[string]ProfileConfig{
			"test": {StackName: "test-stack"},
		},
	}

	_, err := config.GetActiveProfile()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}
