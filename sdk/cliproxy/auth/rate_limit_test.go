package auth

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	internalconfig "github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/registry"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
)

type authSnapshotStore struct {
	mu    sync.Mutex
	saved []*Auth
}

func (s *authSnapshotStore) List(context.Context) ([]*Auth, error) { return nil, nil }
func (s *authSnapshotStore) Delete(context.Context, string) error  { return nil }
func (s *authSnapshotStore) Save(_ context.Context, auth *Auth) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.saved = append(s.saved, auth.Clone())
	return "", nil
}
func (s *authSnapshotStore) LastSaved() *Auth {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.saved) == 0 {
		return nil
	}
	return s.saved[len(s.saved)-1].Clone()
}
func (s *authSnapshotStore) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.saved = nil
}

type rateLimitTestExecutor struct {
	mu    sync.Mutex
	calls []string
}

func (e *rateLimitTestExecutor) Identifier() string { return "test" }

func (e *rateLimitTestExecutor) Execute(ctx context.Context, auth *Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	e.mu.Lock()
	e.calls = append(e.calls, auth.ID)
	e.mu.Unlock()
	return cliproxyexecutor.Response{Payload: []byte(`{"ok":true}`)}, nil
}

func (e *rateLimitTestExecutor) ExecuteStream(ctx context.Context, auth *Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (*cliproxyexecutor.StreamResult, error) {
	ch := make(chan cliproxyexecutor.StreamChunk)
	close(ch)
	return &cliproxyexecutor.StreamResult{Chunks: ch}, nil
}

func (e *rateLimitTestExecutor) Refresh(ctx context.Context, auth *Auth) (*Auth, error) {
	return auth, nil
}

func (e *rateLimitTestExecutor) CountTokens(ctx context.Context, auth *Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	return e.Execute(ctx, auth, req, opts)
}

func (e *rateLimitTestExecutor) HttpRequest(ctx context.Context, auth *Auth, req *http.Request) (*http.Response, error) {
	return nil, nil
}

func (e *rateLimitTestExecutor) CallIDs() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.calls...)
}

func TestManagerExecute_LocalAuthRateLimitSwitchesToNextAuth(t *testing.T) {
	const model = "gpt-5.4"
	registerSchedulerModels(t, "test", model, "auth-a", "auth-b")

	manager := NewManager(nil, &FillFirstSelector{}, nil)
	executor := &rateLimitTestExecutor{}
	manager.RegisterExecutor(executor)
	manager.SetConfig(&internalconfig.Config{
		AuthRateLimit: internalconfig.AuthRateLimit{
			Limit:           5,
			WindowSeconds:   60,
			CooldownSeconds: 60,
		},
	})

	ctx := context.Background()
	for _, auth := range []*Auth{
		{ID: "auth-a", Provider: "test"},
		{ID: "auth-b", Provider: "test"},
	} {
		if _, err := manager.Register(ctx, auth); err != nil {
			t.Fatalf("Register(%s) error = %v", auth.ID, err)
		}
	}

	for i := 0; i < 6; i++ {
		if _, err := manager.Execute(ctx, []string{"test"}, cliproxyexecutor.Request{Model: model}, cliproxyexecutor.Options{}); err != nil {
			t.Fatalf("Execute() #%d error = %v", i, err)
		}
	}

	got := executor.CallIDs()
	want := []string{"auth-a", "auth-a", "auth-a", "auth-a", "auth-a", "auth-b"}
	if len(got) != len(want) {
		t.Fatalf("call count = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("call #%d auth = %q, want %q (all=%v)", i, got[i], want[i], got)
		}
	}

	authA, ok := manager.GetByID("auth-a")
	if !ok {
		t.Fatal("auth-a missing from manager")
	}
	if !authA.LocalRateLimit.CooldownUntil.After(time.Now()) {
		t.Fatalf("auth-a cooldown = %v, want future time", authA.LocalRateLimit.CooldownUntil)
	}
}

func TestManagerExecute_LocalAuthRateLimitRuleOverride(t *testing.T) {
	const model = "gpt-5.4"
	registerSchedulerModels(t, "test", model, "auth-a", "auth-b")

	manager := NewManager(nil, &FillFirstSelector{}, nil)
	executor := &rateLimitTestExecutor{}
	manager.RegisterExecutor(executor)
	manager.SetConfig(&internalconfig.Config{
		AuthRateLimit: internalconfig.AuthRateLimit{
			Limit:           5,
			WindowSeconds:   60,
			CooldownSeconds: 60,
			PerAuthRules:    []string{"user@example.com|limit|2"},
		},
	})

	ctx := context.Background()
	if _, err := manager.Register(ctx, &Auth{
		ID:       "auth-a",
		Provider: "test",
		Metadata: map[string]any{"email": "user@example.com"},
	}); err != nil {
		t.Fatalf("Register(auth-a) error = %v", err)
	}
	if _, err := manager.Register(ctx, &Auth{ID: "auth-b", Provider: "test"}); err != nil {
		t.Fatalf("Register(auth-b) error = %v", err)
	}

	for i := 0; i < 3; i++ {
		if _, err := manager.Execute(ctx, []string{"test"}, cliproxyexecutor.Request{Model: model}, cliproxyexecutor.Options{}); err != nil {
			t.Fatalf("Execute() #%d error = %v", i, err)
		}
	}

	got := executor.CallIDs()
	want := []string{"auth-a", "auth-a", "auth-b"}
	if len(got) != len(want) {
		t.Fatalf("call count = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("call #%d auth = %q, want %q (all=%v)", i, got[i], want[i], got)
		}
	}
}

func TestManagerExecute_LocalAuthRateLimitAuthFileOverride(t *testing.T) {
	const model = "gpt-5.4"
	registerSchedulerModels(t, "test", model, "auth-a", "auth-b")

	manager := NewManager(nil, &FillFirstSelector{}, nil)
	executor := &rateLimitTestExecutor{}
	manager.RegisterExecutor(executor)
	manager.SetConfig(&internalconfig.Config{
		AuthRateLimit: internalconfig.AuthRateLimit{
			Limit:           5,
			WindowSeconds:   60,
			CooldownSeconds: 60,
			PerAuthRules:    []string{"user@example.com|limit|4"},
		},
	})

	ctx := context.Background()
	if _, err := manager.Register(ctx, &Auth{
		ID:       "auth-a",
		Provider: "test",
		Metadata: map[string]any{
			"email":                 "user@example.com",
			"auth_rate_limit_limit": 2,
		},
	}); err != nil {
		t.Fatalf("Register(auth-a) error = %v", err)
	}
	if _, err := manager.Register(ctx, &Auth{ID: "auth-b", Provider: "test"}); err != nil {
		t.Fatalf("Register(auth-b) error = %v", err)
	}

	for i := 0; i < 3; i++ {
		if _, err := manager.Execute(ctx, []string{"test"}, cliproxyexecutor.Request{Model: model}, cliproxyexecutor.Options{}); err != nil {
			t.Fatalf("Execute() #%d error = %v", i, err)
		}
	}

	got := executor.CallIDs()
	want := []string{"auth-a", "auth-a", "auth-b"}
	if len(got) != len(want) {
		t.Fatalf("call count = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("call #%d auth = %q, want %q (all=%v)", i, got[i], want[i], got)
		}
	}
}

func TestManagerSetConfig_ClearsLocalAuthRateLimitState(t *testing.T) {
	const model = "gpt-5.4"
	registerSchedulerModels(t, "test", model, "auth-a")

	manager := NewManager(nil, &FillFirstSelector{}, nil)
	executor := &rateLimitTestExecutor{}
	manager.RegisterExecutor(executor)
	manager.SetConfig(&internalconfig.Config{
		AuthRateLimit: internalconfig.AuthRateLimit{
			Limit:           1,
			WindowSeconds:   60,
			CooldownSeconds: 60,
		},
	})

	ctx := context.Background()
	if _, err := manager.Register(ctx, &Auth{ID: "auth-a", Provider: "test"}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	if _, err := manager.Execute(ctx, []string{"test"}, cliproxyexecutor.Request{Model: model}, cliproxyexecutor.Options{}); err != nil {
		t.Fatalf("first Execute() error = %v", err)
	}
	if allowed, retryAt := manager.reserveAuthRateLimit("auth-a"); allowed || retryAt.IsZero() {
		t.Fatalf("reserveAuthRateLimit() = allowed:%t retryAt:%v, want blocked with retry", allowed, retryAt)
	}

	manager.SetConfig(&internalconfig.Config{})

	authA, ok := manager.GetByID("auth-a")
	if !ok {
		t.Fatal("auth-a missing from manager")
	}
	if !authA.LocalRateLimit.CooldownUntil.IsZero() || len(authA.LocalRateLimit.RequestTimestamps) != 0 {
		t.Fatalf("local rate state = %#v, want cleared", authA.LocalRateLimit)
	}

	reg := registry.GetGlobalRegistry()
	if !reg.ClientSupportsModel("auth-a", model) {
		t.Fatalf("registry lost model registration for auth-a")
	}
}

func TestManagerReserveAuthRateLimit_PersistsCooldownState(t *testing.T) {
	store := &authSnapshotStore{}
	manager := NewManager(store, &FillFirstSelector{}, nil)
	manager.SetConfig(&internalconfig.Config{
		AuthRateLimit: internalconfig.AuthRateLimit{
			Limit:           1,
			WindowSeconds:   60,
			CooldownSeconds: 60,
		},
	})

	ctx := context.Background()
	if _, err := manager.Register(ctx, &Auth{
		ID:       "auth-a",
		Provider: "test",
		Metadata: map[string]any{"type": "test"},
	}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	store.Reset()

	if allowed, _ := manager.reserveAuthRateLimit("auth-a"); !allowed {
		t.Fatalf("first reserveAuthRateLimit() blocked unexpectedly")
	}
	if last := store.LastSaved(); last != nil {
		t.Fatalf("unexpected persistence on first reservation: %#v", last.LocalRateLimit)
	}

	if allowed, retryAt := manager.reserveAuthRateLimit("auth-a"); allowed || retryAt.IsZero() {
		t.Fatalf("second reserveAuthRateLimit() = allowed:%t retryAt:%v, want blocked with retry", allowed, retryAt)
	}

	last := store.LastSaved()
	if last == nil {
		t.Fatal("expected cooldown state to be persisted")
	}
	if !last.LocalRateLimit.CooldownUntil.After(time.Now()) {
		t.Fatalf("persisted cooldown = %v, want future time", last.LocalRateLimit.CooldownUntil)
	}
	if len(last.LocalRateLimit.RequestTimestamps) != 0 {
		t.Fatalf("persisted request timestamps = %d, want cleared during cooldown", len(last.LocalRateLimit.RequestTimestamps))
	}
}
