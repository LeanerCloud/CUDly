package common

import (
	"context"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/organizations"
)

// AccountAliasCache caches account ID to friendly name mappings
type AccountAliasCache struct {
	mu     sync.RWMutex
	cache  map[string]string
	client *organizations.Client
}

// NewAccountAliasCache creates a new account alias cache
func NewAccountAliasCache(cfg aws.Config) *AccountAliasCache {
	return &AccountAliasCache{
		cache:  make(map[string]string),
		client: organizations.NewFromConfig(cfg),
	}
}

// GetAccountAlias returns the friendly name for an account ID
// Returns the account ID if lookup fails or if accountID is empty
func (c *AccountAliasCache) GetAccountAlias(ctx context.Context, accountID string) string {
	if accountID == "" {
		return ""
	}

	// Check cache first with read lock
	c.mu.RLock()
	if alias, ok := c.cache[accountID]; ok {
		c.mu.RUnlock()
		return alias
	}
	c.mu.RUnlock()

	// Acquire write lock for fetch operation
	c.mu.Lock()
	defer c.mu.Unlock()

	// Double-check: another goroutine may have populated the cache
	if alias, ok := c.cache[accountID]; ok {
		return alias
	}

	// Fetch from AWS Organizations
	alias := c.fetchAccountAlias(ctx, accountID)

	// Cache the result
	c.cache[accountID] = alias

	return alias
}

// fetchAccountAlias fetches the account name from AWS Organizations
func (c *AccountAliasCache) fetchAccountAlias(ctx context.Context, accountID string) string {
	input := &organizations.DescribeAccountInput{
		AccountId: aws.String(accountID),
	}

	result, err := c.client.DescribeAccount(ctx, input)
	if err != nil {
		// If we can't fetch the alias, return the ID
		// This might happen if:
		// - Not running from organization management account
		// - Missing organizations:DescribeAccount permission
		// - Single account (not in an organization)
		AppLogger.Printf("    ℹ️  Could not fetch account alias for %s: %v (using ID)\n", accountID, err)
		return accountID
	}

	if result.Account != nil && result.Account.Name != nil {
		return aws.ToString(result.Account.Name)
	}

	return accountID
}

// GetAccountAliasShort returns a shortened, filesystem-safe version of the account alias
// Useful for reservation IDs which have length limits
func (c *AccountAliasCache) GetAccountAliasShort(ctx context.Context, accountID string) string {
	alias := c.GetAccountAlias(ctx, accountID)

	// If it's still the account ID (lookup failed), return empty string
	if alias == accountID {
		return ""
	}

	// Convert to lowercase, replace spaces and special chars with hyphens
	alias = sanitizeForReservationID(alias)

	// Limit length to 20 chars for reservation IDs
	if len(alias) > 20 {
		alias = alias[:20]
	}

	return alias
}

// sanitizeForReservationID makes a string safe for use in reservation IDs
func sanitizeForReservationID(s string) string {
	// Replace spaces and special characters using efficient string building
	var sb strings.Builder
	sb.Grow(len(s)) // Pre-allocate for efficiency
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			sb.WriteRune(r)
		} else if r == ' ' || r == '_' || r == '.' {
			sb.WriteRune('-')
		}
	}
	return sb.String()
}

// PreloadAccountAliases preloads aliases for a list of account IDs
// This is useful to avoid delays during purchase operations
func (c *AccountAliasCache) PreloadAccountAliases(ctx context.Context, accountIDs []string) {
	for _, accountID := range accountIDs {
		if accountID != "" {
			c.GetAccountAlias(ctx, accountID)
		}
	}
	AppLogger.Printf("    ✅ Preloaded %d account aliases\n", len(c.cache))
}
