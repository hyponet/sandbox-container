package middleware

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/hyponet/sandbox-container/audit"

	"github.com/gin-gonic/gin"
)

// APIKeys holds the set of valid API keys loaded from the environment.
var apiKeys map[string]bool

type projectAccessRule struct {
	all      bool
	projects map[string]struct{}
}

var projectAccess map[string]projectAccessRule

const (
	contextAPIKey      = "sandbox_api_key"
	contextAuthChecked = "sandbox_auth_checked"
)

var ErrProjectAccessDenied = errors.New("project access denied")

// LoadAPIKeysFromEnv re-reads SANDBOX_API_KEY and updates the key set.
// Useful for testing or hot-reload scenarios.
func LoadAPIKeysFromEnv() {
	raw := os.Getenv("SANDBOX_API_KEY")
	apiKeys = make(map[string]bool)
	for _, k := range splitEnvKeys(raw) {
		apiKeys[k] = true
	}
	projectAccess = loadProjectAccessFromEnv(os.Getenv("SANDBOX_PROJECT_ACCESS"))
}

// splitEnvKeys parses a comma-separated key string into individual trimmed keys.
func splitEnvKeys(raw string) []string {
	var keys []string
	for _, key := range strings.Split(raw, ",") {
		k := strings.TrimSpace(key)
		if k != "" {
			keys = append(keys, k)
		}
	}
	return keys
}

func loadProjectAccessFromEnv(raw string) map[string]projectAccessRule {
	rules := make(map[string]projectAccessRule)
	if strings.TrimSpace(raw) == "" {
		return rules
	}

	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		parts := strings.SplitN(entry, "=", 2)
		if len(parts) != 2 {
			log.Printf("[WARN] ignoring invalid SANDBOX_PROJECT_ACCESS entry %q: expected api-key=project-a|project-b", entry)
			continue
		}
		key := strings.TrimSpace(parts[0])
		if key == "" {
			log.Printf("[WARN] ignoring invalid SANDBOX_PROJECT_ACCESS entry %q: empty API key", entry)
			continue
		}

		rule := projectAccessRule{projects: make(map[string]struct{})}
		for _, projectID := range strings.FieldsFunc(parts[1], func(r rune) bool {
			return r == '|' || r == ';' || r == ' ' || r == '\t' || r == '\n'
		}) {
			projectID = strings.TrimSpace(projectID)
			if projectID == "" {
				continue
			}
			if projectID == "*" {
				rule.all = true
				continue
			}
			if err := audit.ValidateID(projectID); err != nil {
				log.Printf("[WARN] ignoring invalid project id %q in SANDBOX_PROJECT_ACCESS: %v", projectID, err)
				continue
			}
			rule.projects[projectID] = struct{}{}
		}
		if rule.all || len(rule.projects) > 0 {
			rules[key] = rule
		}
	}
	return rules
}

func init() {
	apiKeys = make(map[string]bool)
	LoadAPIKeysFromEnv()
}

// AuthRequired returns a Gin middleware that validates the Bearer token in the
// Authorization header against the SANDBOX_API_KEY environment variable.
//
// If SANDBOX_API_KEY is not set, authentication is skipped (open mode).
// Multiple keys can be provided via comma-separated values.
func AuthRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set(contextAuthChecked, true)

		// No keys configured — skip auth
		if len(apiKeys) == 0 {
			c.Next()
			return
		}

		token := extractBearerToken(c.GetHeader("Authorization"))
		if token == "" || !apiKeys[token] {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"message": "unauthorized: invalid or missing API key",
			})
			return
		}

		c.Set(contextAPIKey, token)
		c.Next()
	}
}

// AuthorizeProjectAccess verifies that the authenticated API key may access projectID.
// When SANDBOX_PROJECT_ACCESS is unset, configured API keys are treated as admin keys.
// In open mode, projectdata access is denied unless SANDBOX_PROJECT_ACCESS contains
// a wildcard rule such as "*=public-project" or "*=*".
func AuthorizeProjectAccess(c *gin.Context, projectID string) error {
	if projectID == "" {
		return nil
	}
	if err := audit.ValidateID(projectID); err != nil {
		return fmt.Errorf("invalid project_id: %w", err)
	}

	apiKey, hasAPIKey := apiKeyFromContext(c)

	if len(projectAccess) == 0 {
		if len(apiKeys) == 0 {
			return fmt.Errorf("%w: projectdata requires SANDBOX_API_KEY or SANDBOX_PROJECT_ACCESS in open mode", ErrProjectAccessDenied)
		}
		if hasAPIKey && apiKeys[apiKey] {
			return nil
		}
		return fmt.Errorf("%w: missing authenticated API key", ErrProjectAccessDenied)
	}

	if hasAPIKey && ruleAllowsProject(projectAccess[apiKey], projectID) {
		return nil
	}
	if ruleAllowsProject(projectAccess["*"], projectID) {
		return nil
	}
	return fmt.Errorf("%w: project_id %q is not allowed for this API key", ErrProjectAccessDenied, projectID)
}

func apiKeyFromContext(c *gin.Context) (string, bool) {
	value, ok := c.Get(contextAPIKey)
	if !ok {
		return "", false
	}
	apiKey, ok := value.(string)
	return apiKey, ok && apiKey != ""
}

func ruleAllowsProject(rule projectAccessRule, projectID string) bool {
	if rule.all {
		return true
	}
	_, ok := rule.projects[projectID]
	return ok
}

// extractBearerToken extracts the token from "Bearer <token>" format.
func extractBearerToken(header string) string {
	if header == "" {
		return ""
	}
	parts := strings.SplitN(header, " ", 2)
	if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
		return strings.TrimSpace(parts[1])
	}
	// Also accept raw token without "Bearer" prefix
	return strings.TrimSpace(header)
}
