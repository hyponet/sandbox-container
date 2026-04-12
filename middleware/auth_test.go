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
