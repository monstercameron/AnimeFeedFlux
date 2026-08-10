package publish

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"sync"
	"testing"
)

func TestNewEntry_StrongETagIsHexSHA256OfBody(t *testing.T) {
	body := []byte("hello, feed")
	e, err := NewEntry(body, "text/plain", "Mon, 02 Jan 2006 15:04:05 GMT")
	if err != nil {
		t.Fatalf("NewEntry: %v", err)
	}
	sum := sha256.Sum256(body)
	want := `"` + hex.EncodeToString(sum[:]) + `"`
	if e.ETag != want {
		t.Fatalf("ETag = %q, want %q", e.ETag, want)
	}
	// Strong: no weak-validator prefix.
	if bytes.HasPrefix([]byte(e.ETag), []byte("W/")) {
		t.Fatalf("ETag %q looks weak, want strong", e.ETag)
	}
}

func TestNewEntry_GzipRoundTripsToSameBytes(t *testing.T) {
	body := []byte("this is the exact body that must round-trip byte for byte through gzip")
	e, err := NewEntry(body, "text/plain", "")
	if err != nil {
		t.Fatalf("NewEntry: %v", err)
	}
	if len(e.GzipBody) == 0 {
		t.Fatal("GzipBody is empty")
	}
	zr, err := gzip.NewReader(bytes.NewReader(e.GzipBody))
	if err != nil {
		t.Fatalf("gzip.NewReader: %v", err)
	}
	got, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("reading gzip body: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Fatalf("gzip round-trip = %q, want %q", got, body)
	}
}

func TestCache_GetPutMiss(t *testing.T) {
	c := NewCache()
	if _, ok := c.Get("nope"); ok {
		t.Fatal("expected miss on empty cache")
	}
	e := Entry{Body: []byte("x"), ContentType: "text/plain"}
	c.Put("k", e)
	got, ok := c.Get("k")
	if !ok {
		t.Fatal("expected hit after Put")
	}
	if string(got.Body) != "x" {
		t.Fatalf("Body = %q, want %q", got.Body, "x")
	}
}

func TestCache_InvalidateDropsEveryFormatForSlug(t *testing.T) {
	c := NewCache()
	c.Put("trivia:xml", Entry{Body: []byte("1")})
	c.Put("trivia:atom", Entry{Body: []byte("2")})
	c.Put("trivia:json", Entry{Body: []byte("3")})
	c.Put("trivia:item:01ABC", Entry{Body: []byte("4")})
	c.Put("news:xml", Entry{Body: []byte("5")})
	c.Put("index", Entry{Body: []byte("6")})

	c.Invalidate("trivia")

	for _, k := range []string{"trivia:xml", "trivia:atom", "trivia:json", "trivia:item:01ABC", "index"} {
		if _, ok := c.Get(k); ok {
			t.Errorf("key %q survived Invalidate(\"trivia\")", k)
		}
	}
	if _, ok := c.Get("news:xml"); !ok {
		t.Error("Invalidate(\"trivia\") dropped an unrelated feed's entry")
	}
}

func TestCache_InvalidateDoesNotMatchUnrelatedPrefix(t *testing.T) {
	// "trivia-daily" must not be swept up by Invalidate("trivia").
	c := NewCache()
	c.Put("trivia-daily:xml", Entry{Body: []byte("1")})
	c.Invalidate("trivia")
	if _, ok := c.Get("trivia-daily:xml"); !ok {
		t.Fatal("Invalidate(\"trivia\") incorrectly matched \"trivia-daily:xml\"")
	}
}

func TestCache_InvalidateAllDropsEverything(t *testing.T) {
	c := NewCache()
	c.Put("a:xml", Entry{})
	c.Put("b:atom", Entry{})
	c.InvalidateAll()
	if s := c.Stats(); s.Entries != 0 {
		t.Fatalf("Entries = %d, want 0 after InvalidateAll", s.Entries)
	}
}

func TestCache_Stats(t *testing.T) {
	c := NewCache()
	e1, _ := NewEntry([]byte("abcd"), "text/plain", "")
	e2, _ := NewEntry([]byte("efghij"), "text/plain", "")
	c.Put("a", e1)
	c.Put("b", e2)

	s := c.Stats()
	if s.Entries != 2 {
		t.Fatalf("Entries = %d, want 2", s.Entries)
	}
	if s.BodyBytes != int64(len("abcd")+len("efghij")) {
		t.Fatalf("BodyBytes = %d, want %d", s.BodyBytes, len("abcd")+len("efghij"))
	}
	if s.GzipBodyBytes == 0 {
		t.Fatal("GzipBodyBytes = 0, want > 0")
	}
}

func TestCache_ConcurrentUse(t *testing.T) {
	c := NewCache()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(3)
		go func(i int) {
			defer wg.Done()
			c.Put("k", Entry{Body: []byte("v")})
		}(i)
		go func(i int) {
			defer wg.Done()
			c.Get("k")
		}(i)
		go func(i int) {
			defer wg.Done()
			if i%10 == 0 {
				c.Invalidate("k")
			}
		}(i)
	}
	wg.Wait()
}
