package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestRampSchedule_GetCurrentCoverage(t *testing.T) {
	tests := []struct {
		name         string
		schedule     RampSchedule
		baseCoverage float64
		expected     float64
	}{
		{
			name: "immediate schedule returns full coverage",
			schedule: RampSchedule{
				Type: "immediate",
			},
			baseCoverage: 80,
			expected:     80,
		},
		{
			name: "weekly 25% at step 0",
			schedule: RampSchedule{
				Type:           "weekly",
				PercentPerStep: 25,
				CurrentStep:    0,
			},
			baseCoverage: 80,
			expected:     0,
		},
		{
			name: "weekly 25% at step 1",
			schedule: RampSchedule{
				Type:           "weekly",
				PercentPerStep: 25,
				CurrentStep:    1,
			},
			baseCoverage: 80,
			expected:     20, // 80 * 25 / 100
		},
		{
			name: "weekly 25% at step 2",
			schedule: RampSchedule{
				Type:           "weekly",
				PercentPerStep: 25,
				CurrentStep:    2,
			},
			baseCoverage: 80,
			expected:     40, // 80 * 50 / 100
		},
		{
			name: "weekly 25% at step 4 (100%)",
			schedule: RampSchedule{
				Type:           "weekly",
				PercentPerStep: 25,
				CurrentStep:    4,
			},
			baseCoverage: 80,
			expected:     80, // 80 * 100 / 100
		},
		{
			name: "monthly 10% at step 5",
			schedule: RampSchedule{
				Type:           "monthly",
				PercentPerStep: 10,
				CurrentStep:    5,
			},
			baseCoverage: 100,
			expected:     50, // 100 * 50 / 100
		},
		{
			name: "coverage capped at 100%",
			schedule: RampSchedule{
				Type:           "weekly",
				PercentPerStep: 50,
				CurrentStep:    5, // 250% but capped
			},
			baseCoverage: 80,
			expected:     80, // capped at 100%
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.schedule.GetCurrentCoverage(tt.baseCoverage)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestRampSchedule_GetNextPurchaseDate(t *testing.T) {
	now := time.Now()
	startDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name     string
		schedule RampSchedule
		expected time.Time
	}{
		{
			name: "zero start date returns now",
			schedule: RampSchedule{
				Type:        "weekly",
				CurrentStep: 0,
			},
			expected: now,
		},
		{
			name: "step 0 returns start date",
			schedule: RampSchedule{
				Type:             "weekly",
				StepIntervalDays: 7,
				CurrentStep:      0,
				StartDate:        startDate,
			},
			expected: startDate,
		},
		{
			name: "step 1 weekly",
			schedule: RampSchedule{
				Type:             "weekly",
				StepIntervalDays: 7,
				CurrentStep:      1,
				StartDate:        startDate,
			},
			expected: startDate.AddDate(0, 0, 7),
		},
		{
			name: "step 3 monthly",
			schedule: RampSchedule{
				Type:             "monthly",
				StepIntervalDays: 30,
				CurrentStep:      3,
				StartDate:        startDate,
			},
			expected: startDate.AddDate(0, 0, 90),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.schedule.GetNextPurchaseDate()
			if tt.schedule.StartDate.IsZero() {
				// For zero start date, just check it's close to now
				assert.WithinDuration(t, now, result, time.Second)
			} else {
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}

func TestRampSchedule_IsComplete(t *testing.T) {
	tests := []struct {
		name     string
		schedule RampSchedule
		expected bool
	}{
		{
			name: "not complete - step 0 of 4",
			schedule: RampSchedule{
				CurrentStep: 0,
				TotalSteps:  4,
			},
			expected: false,
		},
		{
			name: "not complete - step 2 of 4",
			schedule: RampSchedule{
				CurrentStep: 2,
				TotalSteps:  4,
			},
			expected: false,
		},
		{
			name: "complete - step 4 of 4",
			schedule: RampSchedule{
				CurrentStep: 4,
				TotalSteps:  4,
			},
			expected: true,
		},
		{
			name: "complete - step 5 of 4 (over)",
			schedule: RampSchedule{
				CurrentStep: 5,
				TotalSteps:  4,
			},
			expected: true,
		},
		{
			name: "immediate is complete at step 1",
			schedule: RampSchedule{
				Type:        "immediate",
				CurrentStep: 1,
				TotalSteps:  1,
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.schedule.IsComplete()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestPresetRampSchedules(t *testing.T) {
	t.Run("immediate schedule exists and is correct", func(t *testing.T) {
		schedule, exists := PresetRampSchedules["immediate"]
		assert.True(t, exists)
		assert.Equal(t, "immediate", schedule.Type)
		assert.Equal(t, float64(100), schedule.PercentPerStep)
		assert.Equal(t, 1, schedule.TotalSteps)
	})

	t.Run("weekly-25pct schedule exists and is correct", func(t *testing.T) {
		schedule, exists := PresetRampSchedules["weekly-25pct"]
		assert.True(t, exists)
		assert.Equal(t, "weekly", schedule.Type)
		assert.Equal(t, float64(25), schedule.PercentPerStep)
		assert.Equal(t, 7, schedule.StepIntervalDays)
		assert.Equal(t, 4, schedule.TotalSteps)
	})

	t.Run("monthly-10pct schedule exists and is correct", func(t *testing.T) {
		schedule, exists := PresetRampSchedules["monthly-10pct"]
		assert.True(t, exists)
		assert.Equal(t, "monthly", schedule.Type)
		assert.Equal(t, float64(10), schedule.PercentPerStep)
		assert.Equal(t, 30, schedule.StepIntervalDays)
		assert.Equal(t, 10, schedule.TotalSteps)
	})
}

func TestGlobalConfig_Defaults(t *testing.T) {
	cfg := GlobalConfig{}

	assert.Empty(t, cfg.EnabledProviders)
	assert.Empty(t, cfg.NotificationEmail)
	assert.False(t, cfg.ApprovalRequired)
	assert.Equal(t, 0, cfg.DefaultTerm)
	assert.Empty(t, cfg.DefaultPayment)
	assert.Equal(t, float64(0), cfg.DefaultCoverage)
	assert.Empty(t, cfg.DefaultRampSchedule)
}

func TestServiceConfig_Defaults(t *testing.T) {
	cfg := ServiceConfig{}

	assert.Empty(t, cfg.Provider)
	assert.Empty(t, cfg.Service)
	assert.False(t, cfg.Enabled)
	assert.Equal(t, 0, cfg.Term)
	assert.Empty(t, cfg.Payment)
	assert.Equal(t, float64(0), cfg.Coverage)
	assert.Empty(t, cfg.RampSchedule)
}

func TestPurchasePlan_Defaults(t *testing.T) {
	plan := PurchasePlan{}

	assert.Empty(t, plan.ID)
	assert.Empty(t, plan.Name)
	assert.False(t, plan.Enabled)
	assert.False(t, plan.AutoPurchase)
	assert.Equal(t, 0, plan.NotificationDaysBefore)
	assert.Nil(t, plan.Services)
	assert.True(t, plan.CreatedAt.IsZero())
	assert.True(t, plan.UpdatedAt.IsZero())
	assert.Nil(t, plan.NextExecutionDate)
	assert.Nil(t, plan.LastExecutionDate)
}

func TestPurchaseExecution_Statuses(t *testing.T) {
	validStatuses := []string{"pending", "notified", "approved", "cancelled", "completed", "failed"}

	for _, status := range validStatuses {
		exec := PurchaseExecution{Status: status}
		assert.Equal(t, status, exec.Status)
	}
}

func TestRecommendationRecord_Fields(t *testing.T) {
	rec := RecommendationRecord{
		ID:           "rec-123",
		Provider:     "aws",
		Service:      "rds",
		Region:       "us-east-1",
		ResourceType: "db.r5.large",
		Engine:       "postgres",
		Count:        2,
		Term:         3,
		Payment:      "all-upfront",
		UpfrontCost:  1500.00,
		MonthlyCost:  0,
		Savings:      200.00,
		Selected:     true,
		Purchased:    false,
	}

	assert.Equal(t, "rec-123", rec.ID)
	assert.Equal(t, "aws", rec.Provider)
	assert.Equal(t, "rds", rec.Service)
	assert.Equal(t, "us-east-1", rec.Region)
	assert.Equal(t, "db.r5.large", rec.ResourceType)
	assert.Equal(t, "postgres", rec.Engine)
	assert.Equal(t, 2, rec.Count)
	assert.Equal(t, 3, rec.Term)
	assert.Equal(t, "all-upfront", rec.Payment)
	assert.Equal(t, 1500.00, rec.UpfrontCost)
	assert.Equal(t, float64(0), rec.MonthlyCost)
	assert.Equal(t, 200.00, rec.Savings)
	assert.True(t, rec.Selected)
	assert.False(t, rec.Purchased)
}

func TestPurchaseHistoryRecord_Fields(t *testing.T) {
	now := time.Now()
	rec := PurchaseHistoryRecord{
		AccountID:        "123456789012",
		PurchaseID:       "purchase-abc",
		Timestamp:        now,
		Provider:         "aws",
		Service:          "ec2",
		Region:           "eu-west-1",
		ResourceType:     "m5.xlarge",
		Count:            5,
		Term:             1,
		Payment:          "no-upfront",
		UpfrontCost:      0,
		MonthlyCost:      150.00,
		EstimatedSavings: 50.00,
		PlanID:           "plan-123",
		PlanName:         "EC2 Production Plan",
		RampStep:         1,
	}

	assert.Equal(t, "123456789012", rec.AccountID)
	assert.Equal(t, "purchase-abc", rec.PurchaseID)
	assert.Equal(t, now, rec.Timestamp)
	assert.Equal(t, "aws", rec.Provider)
	assert.Equal(t, "ec2", rec.Service)
	assert.Equal(t, "eu-west-1", rec.Region)
	assert.Equal(t, "m5.xlarge", rec.ResourceType)
	assert.Equal(t, 5, rec.Count)
	assert.Equal(t, 1, rec.Term)
	assert.Equal(t, "no-upfront", rec.Payment)
	assert.Equal(t, float64(0), rec.UpfrontCost)
	assert.Equal(t, 150.00, rec.MonthlyCost)
	assert.Equal(t, 50.00, rec.EstimatedSavings)
	assert.Equal(t, "plan-123", rec.PlanID)
	assert.Equal(t, "EC2 Production Plan", rec.PlanName)
	assert.Equal(t, 1, rec.RampStep)
}
