package credential

import (
	"context"
	"errors"
	"sync"
	"testing"
)

func TestMemoryStoreLifecycle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := NewMemoryStore()
	if _, err := store.Get(ctx, "work", KindClientSecret); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get absent: %v", err)
	}
	if err := store.Set(ctx, "work", KindClientSecret, "secret"); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(ctx, "work", KindClientSecret)
	if err != nil || got != "secret" {
		t.Fatalf("Get = %q, %v", got, err)
	}
	if err := store.Delete(ctx, "work", KindClientSecret); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(ctx, "work", KindClientSecret); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get deleted: %v", err)
	}
}

func TestMemoryStoreConcurrentAccess(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := NewMemoryStore()
	var wait sync.WaitGroup
	for range 32 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for range 100 {
				if err := store.Set(ctx, "work", KindOAuthToken, "token"); err != nil {
					t.Errorf("Set: %v", err)
				}
				if _, err := store.Get(ctx, "work", KindOAuthToken); err != nil {
					t.Errorf("Get: %v", err)
				}
			}
		}()
	}
	wait.Wait()
}

func TestMemoryStoreValidatesInput(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	if err := store.Set(context.Background(), "../escape", KindClientSecret, "x"); err == nil {
		t.Fatal("expected invalid profile error")
	}
	if err := store.Set(context.Background(), "work", Kind("other"), "x"); err == nil {
		t.Fatal("expected invalid kind error")
	}
	if err := store.Set(context.Background(), "work", KindClientSecret, ""); err == nil {
		t.Fatal("expected empty credential error")
	}
}

func TestMemoryStoreZeroValue(t *testing.T) {
	t.Parallel()
	var store MemoryStore
	if err := store.Set(context.Background(), "work", KindClientSecret, "secret"); err != nil {
		t.Fatal(err)
	}
	if value, err := store.Get(context.Background(), "work", KindClientSecret); err != nil || value != "secret" {
		t.Fatalf("Get = %q, %v", value, err)
	}
}
