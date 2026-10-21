package blob_test

import (
	"context"
	"errors"
	"siplatform/internal/blob"
	"strings"
	"testing"
	"time"
)

func TestLocalDeletionGuardFencesOtherStorePublication(t *testing.T) {
	root := t.TempDir()
	a, err := blob.NewLocal(root)
	if err != nil {
		t.Fatal(err)
	}
	b, err := blob.NewLocal(root)
	if err != nil {
		t.Fatal(err)
	}
	key := keyFor("abc")
	if err = a.Put(context.Background(), key, strings.NewReader("abc"), 3, "text/plain"); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	finished := make(chan error, 1)
	lost := errors.New("database guard lost")
	go func() {
		finished <- blob.DeleteGuarded(context.Background(), a, key, func(context.Context) error { close(entered); <-release; return lost })
	}()
	<-entered
	put := make(chan error, 1)
	go func() { put <- b.Put(context.Background(), key, strings.NewReader("abc"), 3, "application/json") }()
	select {
	case err := <-put:
		close(release)
		<-finished
		t.Fatalf("publication crossed deletion fence: %v", err)
	case <-time.After(75 * time.Millisecond):
	}
	close(release)
	if err = <-finished; !errors.Is(err, lost) {
		t.Fatal(err)
	}
	if err = <-put; err != nil {
		t.Fatal(err)
	}
	m, err := b.Stat(context.Background(), key)
	if err != nil || m.ContentType != "application/json" {
		t.Fatalf("replacement lost: %+v %v", m, err)
	}
}
