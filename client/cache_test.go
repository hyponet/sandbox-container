package client

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// =============================================
// Cache key tests
// =============================================

func TestAgentListCacheKey_OrderIndependent(t *testing.T) {
	req1 := &agentSkillRequest{SkillIDs: []string{"b", "a", "c"}}
	req2 := &agentSkillRequest{SkillIDs: []string{"c", "a", "b"}}

	k1 := agentListCacheKey("agent1", req1)
	k2 := agentListCacheKey("agent1", req2)

	if k1 != k2 {
		t.Errorf("keys should be equal regardless of skillID order: %q vs %q", k1, k2)
	}
}

func TestAgentListCacheKey_DifferentAgents(t *testing.T) {
	req := &agentSkillRequest{SkillIDs: []string{"s1"}}

	k1 := agentListCacheKey("agent1", req)
	k2 := agentListCacheKey("agent2", req)

	if k1 == k2 {
		t.Error("keys for different agents should differ")
	}
}

func TestAgentListCacheKey_OptionsAffectKey(t *testing.T) {
	base := &agentSkillRequest{SkillIDs: []string{"s1"}}
	withCleanup := &agentSkillRequest{SkillIDs: []string{"s1"}, Cleanup: true}
	withWritable := &agentSkillRequest{SkillIDs: []string{"s1"}, EnableAgentWorkspace: true}

	kBase := agentListCacheKey("a", base)
	kCleanup := agentListCacheKey("a", withCleanup)
	kWritable := agentListCacheKey("a", withWritable)

	if kBase == kCleanup {
		t.Error("cleanup flag should change the key")
	}
	if kBase == kWritable {
		t.Error("writable flag should change the key")
	}
	if kCleanup == kWritable {
		t.Error("cleanup and writable should produce different keys")
	}
}

func TestAgentListCacheKey_NoCommaCollision(t *testing.T) {
	// Ensure ["a,b"] and ["a","b"] produce different keys.
	req1 := &agentSkillRequest{SkillIDs: []string{"a,b"}}
	req2 := &agentSkillRequest{SkillIDs: []string{"a", "b"}}

	k1 := agentListCacheKey("x", req1)
	k2 := agentListCacheKey("x", req2)

	if k1 == k2 {
		t.Error("skill IDs containing commas should not collide with separate IDs")
	}
}

// =============================================
// Cache hit / miss / expiry (integration tests)
// =============================================

func TestSkillAgentList_CacheHit(t *testing.T) {
	cli, cleanup := setupTestServer(t)
	defer cleanup()

	createAndDeploySkill(t, cli, "cache-hit", "test")

	// First call — cache miss, hits server.
	r1, err := cli.SkillAgentList("a1", []string{"cache-hit"})
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	if len(r1.Skills) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(r1.Skills))
	}

	// Second call — should return cached result (same content).
	r2, err := cli.SkillAgentList("a1", []string{"cache-hit"})
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if len(r2.Skills) != 1 || r2.Skills[0].Name != r1.Skills[0].Name {
		t.Errorf("cached result mismatch: %+v vs %+v", r1.Skills, r2.Skills)
	}
}

func TestSkillAgentList_CacheExpiry(t *testing.T) {
	cli, cleanup := setupTestServer(t)
	defer cleanup()

	// Use a very short TTL so we can test expiry.
	cli.agentListTTL = 50 * time.Millisecond

	createAndDeploySkill(t, cli, "cache-exp", "test")

	_, err := cli.SkillAgentList("a1", []string{"cache-exp"})
	if err != nil {
		t.Fatalf("first call: %v", err)
	}

	// Verify cache is populated.
	cli.agentListMu.Lock()
	count := len(cli.agentListCache)
	cli.agentListMu.Unlock()
	if count == 0 {
		t.Fatal("cache should have an entry after first call")
	}

	// Wait for expiry.
	time.Sleep(100 * time.Millisecond)

	// Next call should miss cache and re-fetch.
	r, err := cli.SkillAgentList("a1", []string{"cache-exp"})
	if err != nil {
		t.Fatalf("after expiry: %v", err)
	}
	if len(r.Skills) != 1 {
		t.Errorf("expected 1 skill after re-fetch, got %d", len(r.Skills))
	}
}

func TestSkillAgentList_CustomTTL(t *testing.T) {
	cli, cleanup := setupTestServer(t)
	defer cleanup()

	// Override TTL via option — verify it takes effect.
	cli.agentListTTL = 1 * time.Hour

	createAndDeploySkill(t, cli, "ttl-test", "test")
	_, _ = cli.SkillAgentList("a1", []string{"ttl-test"})

	cli.agentListMu.Lock()
	var ttlOK bool
	for _, entry := range cli.agentListCache {
		// Entry should expire ~1h from now, not 5min.
		if time.Until(entry.expiresAt) > 30*time.Minute {
			ttlOK = true
		}
	}
	cli.agentListMu.Unlock()

	if !ttlOK {
		t.Error("cache entry TTL should reflect the custom 1h setting")
	}
}

func TestNewClient_WithAgentListCacheTTL(t *testing.T) {
	cli := NewClient("http://localhost:0", WithAgentListCacheTTL(10*time.Second))
	if cli.agentListTTL != 10*time.Second {
		t.Errorf("expected TTL 10s, got %v", cli.agentListTTL)
	}
}

// =============================================
// Deep copy test
// =============================================

func TestSkillAgentList_DeepCopy(t *testing.T) {
	cli, cleanup := setupTestServer(t)
	defer cleanup()

	createAndDeploySkill(t, cli, "copy-test", "test")

	r1, err := cli.SkillAgentList("a1", []string{"copy-test"})
	if err != nil {
		t.Fatalf("first call: %v", err)
	}

	// Mutate the returned slice.
	r1.Skills = append(r1.Skills, SkillSummary{Name: "injected"})

	// Second call should NOT see the mutation.
	r2, err := cli.SkillAgentList("a1", []string{"copy-test"})
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	for _, s := range r2.Skills {
		if s.Name == "injected" {
			t.Fatal("caller mutation leaked into cache")
		}
	}
}

// =============================================
// Invalidation tests
// =============================================

func TestSkillAgentCacheDelete_InvalidatesLocalCache(t *testing.T) {
	cli, cleanup := setupTestServer(t)
	defer cleanup()

	createAndDeploySkill(t, cli, "inv-test", "test")

	// Populate cache.
	_, err := cli.SkillAgentList("inv-agent", []string{"inv-test"})
	if err != nil {
		t.Fatalf("SkillAgentList: %v", err)
	}

	cli.agentListMu.Lock()
	before := len(cli.agentListCache)
	cli.agentListMu.Unlock()
	if before == 0 {
		t.Fatal("cache should be populated")
	}

	// Delete server cache — should also clear local cache for this agent.
	_, err = cli.SkillAgentCacheDelete("inv-agent", "inv-test")
	if err != nil {
		t.Fatalf("SkillAgentCacheDelete: %v", err)
	}

	cli.agentListMu.Lock()
	after := len(cli.agentListCache)
	cli.agentListMu.Unlock()
	if after != 0 {
		t.Errorf("local cache should be empty after invalidation, got %d entries", after)
	}
}

func TestInvalidateAgentListCache_OnlyAffectsTargetAgent(t *testing.T) {
	cli, cleanup := setupTestServer(t)
	defer cleanup()

	createAndDeploySkill(t, cli, "iso-a", "test")
	createAndDeploySkill(t, cli, "iso-b", "test")

	_, _ = cli.SkillAgentList("agent-x", []string{"iso-a"})
	_, _ = cli.SkillAgentList("agent-y", []string{"iso-b"})

	cli.agentListMu.Lock()
	if len(cli.agentListCache) != 2 {
		t.Fatalf("expected 2 cache entries, got %d", len(cli.agentListCache))
	}
	cli.agentListMu.Unlock()

	// Invalidate only agent-x.
	cli.invalidateAgentListCache("agent-x")

	cli.agentListMu.Lock()
	remaining := len(cli.agentListCache)
	cli.agentListMu.Unlock()
	if remaining != 1 {
		t.Errorf("expected 1 remaining entry (agent-y), got %d", remaining)
	}
}

// =============================================
// Eviction test
// =============================================

func TestEvictExpiredEntries(t *testing.T) {
	cli, cleanup := setupTestServer(t)
	defer cleanup()

	cli.agentListTTL = 10 * time.Millisecond

	createAndDeploySkill(t, cli, "evict-a", "test")
	createAndDeploySkill(t, cli, "evict-b", "test")

	_, _ = cli.SkillAgentList("a1", []string{"evict-a"})
	_, _ = cli.SkillAgentList("a1", []string{"evict-b"})

	// Wait for entries to expire.
	time.Sleep(50 * time.Millisecond)

	// Trigger eviction by making a new call (which writes to cache).
	createAndDeploySkill(t, cli, "evict-c", "test")
	_, _ = cli.SkillAgentList("a1", []string{"evict-c"})

	cli.agentListMu.Lock()
	count := len(cli.agentListCache)
	cli.agentListMu.Unlock()

	// Only the fresh entry for evict-c should remain.
	if count != 1 {
		t.Errorf("expected 1 entry after eviction, got %d", count)
	}
}

// =============================================
// Singleflight test
// =============================================

func TestSkillAgentList_Singleflight(t *testing.T) {
	cli, cleanup := setupTestServer(t)
	defer cleanup()

	createAndDeploySkill(t, cli, "sf-test", "test")

	// Track how many HTTP requests actually reach the server by counting
	// cache entries written. We use a short TTL so nothing is cached beforehand.
	cli.agentListTTL = 5 * time.Minute

	const goroutines = 20
	var wg sync.WaitGroup
	var errCount atomic.Int32

	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			r, err := cli.SkillAgentList("sf-agent", []string{"sf-test"})
			if err != nil {
				errCount.Add(1)
				return
			}
			if len(r.Skills) != 1 {
				errCount.Add(1)
			}
		}()
	}
	wg.Wait()

	if errCount.Load() > 0 {
		t.Errorf("%d goroutines got errors", errCount.Load())
	}

	// All goroutines should have gotten a valid result.
	// The singleflight ensures at most one HTTP request was made.
	// We can't directly assert the request count without instrumenting the server,
	// but we verify correctness: cache should have exactly 1 entry.
	cli.agentListMu.Lock()
	count := len(cli.agentListCache)
	cli.agentListMu.Unlock()
	if count != 1 {
		t.Errorf("expected 1 cache entry, got %d", count)
	}
}
