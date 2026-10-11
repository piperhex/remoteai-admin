package accounts

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestModelCatalogPrefetchRetainsOldCatalogOnUpstreamFailures(t *testing.T) {
	s := modelCacheTestService(t)
	ctx := t.Context()
	good := &modelCatalogSnapshot{Catalog: json.RawMessage(`{"models":[{"slug":"good"}]}`)}
	if err := s.saveModelCatalog(ctx, good, ""); err != nil {
		t.Fatal(err)
	}
	for _, failure := range []struct {
		body string
		err  error
	}{
		{`{"models":[]}`, nil}, {`[]`, nil}, {`{"models":[{}]}`, nil},
		{`not json`, nil}, {``, errors.New("upstream timeout")},
	} {
		loader := func(context.Context, string) (*modelCatalogSnapshot, error) {
			return &modelCatalogSnapshot{Catalog: json.RawMessage(failure.body)}, failure.err
		}
		if _, err := s.refreshModelCatalogWith(ctx, true, loader); err == nil {
			t.Fatal("invalid refresh accepted")
		}
		actual, err := s.readModelCatalog(ctx)
		if err != nil || actual.Revision != good.Revision {
			t.Fatal("failed refresh replaced cache", err)
		}
	}
}

func TestModelCatalogSlowRefreshDoesNotBlockReadsOrOverwriteManualEdit(t *testing.T) {
	s := modelCacheTestService(t)
	ctx := t.Context()
	started, resume, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	loader := func(ctx context.Context, _ string) (*modelCatalogSnapshot, error) {
		close(started)
		select {
		case <-resume:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return &modelCatalogSnapshot{Catalog: json.RawMessage(`{"models":[{"slug":"slow"}]}`)}, nil
	}
	go func() { _, err := s.refreshModelCatalogWith(ctx, false, loader); done <- err }()
	<-started
	manual := &modelCatalogSnapshot{Catalog: json.RawMessage(`{"models":[{"slug":"manual"}]}`)}
	if err := s.saveModelCatalog(ctx, manual, ""); err != nil {
		t.Fatal(err)
	}
	if actual, err := s.readModelCatalog(ctx); err != nil || actual.Revision != manual.Revision {
		t.Fatal("reader blocked or lost manual edit", err)
	}
	close(resume)
	if <-done == nil {
		t.Fatal("slow refresh overwrote the manual edit")
	}
	actual, err := s.readModelCatalog(ctx)
	if err != nil || actual.Revision != manual.Revision {
		t.Fatal("manual edit was lost", err)
	}
}

func TestModelCatalogStartupPrefetchAndThirtyMinuteReuse(t *testing.T) {
	s := modelCacheTestService(t)
	calls := 0
	loader := func(context.Context, string) (*modelCatalogSnapshot, error) {
		calls++
		return &modelCatalogSnapshot{Catalog: json.RawMessage(`{"models":[{"slug":"prefetched"}]}`)}, nil
	}
	for i := 0; i < 3; i++ {
		if _, err := s.refreshModelCatalogWith(t.Context(), false, loader); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Fatalf("expected one prefetch, got %d", calls)
	}
	if _, err := s.readModelCatalog(t.Context()); err != nil {
		t.Fatal(err)
	}
}
