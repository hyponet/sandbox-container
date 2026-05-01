package middleware

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/gin-gonic/gin"
)

func setupRouter(auth gin.HandlerFunc) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(auth)
	r.GET("/test", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"success": true})
	})
	return r
}

func setKeys(keys string) {
	os.Setenv("SANDBOX_API_KEY", keys)
	LoadAPIKeysFromEnv()
}

func clearKeys() {
	os.Unsetenv("SANDBOX_API_KEY")
	os.Unsetenv("SANDBOX_PROJECT_ACCESS")
	LoadAPIKeysFromEnv()
}

func setKeysAndProjects(keys, access string) {
	os.Setenv("SANDBOX_API_KEY", keys)
	os.Setenv("SANDBOX_PROJECT_ACCESS", access)
	LoadAPIKeysFromEnv()
}

func TestAuthRequired_OpenMode(t *testing.T) {
	clearKeys()
	r := setupRouter(AuthRequired())

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("open mode: expected 200, got %d", w.Code)
	}
}

func TestAuthRequired_ValidBearer(t *testing.T) {
	setKeys("sk-test-key")
	defer clearKeys()

	r := setupRouter(AuthRequired())

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Bearer sk-test-key")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("valid bearer: expected 200, got %d, body: %s", w.Code, w.Body.String())
	}
}

func TestAuthRequired_InvalidKey(t *testing.T) {
	setKeys("sk-test-key")
	defer clearKeys()

	r := setupRouter(AuthRequired())

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Bearer wrong-key")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("invalid key: expected 401, got %d", w.Code)
	}
}

func TestAuthRequired_MissingHeader(t *testing.T) {
	setKeys("sk-test-key")
	defer clearKeys()

	r := setupRouter(AuthRequired())

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("missing header: expected 401, got %d", w.Code)
	}
}

func TestAuthRequired_MultipleKeys(t *testing.T) {
	setKeys("sk-key-1, sk-key-2, sk-key-3")
	defer clearKeys()

	r := setupRouter(AuthRequired())

	cases := []struct {
		key      string
		expected int
	}{
		{"sk-key-1", http.StatusOK},
		{"sk-key-2", http.StatusOK},
		{"sk-key-3", http.StatusOK},
		{"sk-key-4", http.StatusUnauthorized},
	}

	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.Header.Set("Authorization", "Bearer "+tc.key)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != tc.expected {
			t.Errorf("key=%q: expected %d, got %d", tc.key, tc.expected, w.Code)
		}
	}
}

func TestAuthRequired_RawTokenWithoutBearer(t *testing.T) {
	setKeys("sk-raw")
	defer clearKeys()

	r := setupRouter(AuthRequired())

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "sk-raw")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("raw token: expected 200, got %d", w.Code)
	}
}

func TestAuthRequired_EmptyBearer(t *testing.T) {
	setKeys("sk-test-key")
	defer clearKeys()

	r := setupRouter(AuthRequired())

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Bearer ")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("empty bearer: expected 401, got %d", w.Code)
	}
}

func TestAuthorizeProjectAccess_ConfiguredRules(t *testing.T) {
	setKeysAndProjects("sk-one,sk-two", "sk-one=proj-a|proj-b, sk-two=proj-c")
	defer clearKeys()

	r := gin.New()
	r.Use(AuthRequired())
	r.GET("/project", func(c *gin.Context) {
		if err := AuthorizeProjectAccess(c, c.Query("project_id")); err != nil {
			c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": true})
	})

	tests := []struct {
		name      string
		key       string
		projectID string
		want      int
	}{
		{name: "allowed", key: "sk-one", projectID: "proj-a", want: http.StatusOK},
		{name: "denied", key: "sk-one", projectID: "proj-c", want: http.StatusForbidden},
		{name: "other key allowed", key: "sk-two", projectID: "proj-c", want: http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/project?project_id="+tt.projectID, nil)
			req.Header.Set("Authorization", "Bearer "+tt.key)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != tt.want {
				t.Fatalf("expected %d, got %d: %s", tt.want, w.Code, w.Body.String())
			}
		})
	}
}

func TestAuthorizeProjectAccess_OpenModeRequiresExplicitWildcard(t *testing.T) {
	clearKeys()

	r := gin.New()
	r.Use(AuthRequired())
	r.GET("/project", func(c *gin.Context) {
		if err := AuthorizeProjectAccess(c, "proj-a"); err != nil {
			c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": true})
	})

	req := httptest.NewRequest(http.MethodGet, "/project", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 in open mode without project rules, got %d", w.Code)
	}

	os.Setenv("SANDBOX_PROJECT_ACCESS", "*=proj-a")
	LoadAPIKeysFromEnv()
	defer clearKeys()

	req = httptest.NewRequest(http.MethodGet, "/project", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected wildcard project access, got %d: %s", w.Code, w.Body.String())
	}
}
