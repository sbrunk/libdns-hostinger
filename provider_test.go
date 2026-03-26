package hostinger_test

import (
	"context"
	"net/netip"
	"os"
	"testing"
	"time"

	"github.com/libdns/libdns"
	hostinger "github.com/sbrunk/libdns-hostinger"
)

func newProvider(t *testing.T) (*hostinger.Provider, string) {
	t.Helper()
	token := os.Getenv("HOSTINGER_API_TOKEN")
	zone := os.Getenv("HOSTINGER_TEST_ZONE")
	if token == "" || zone == "" {
		t.Skip("Set HOSTINGER_API_TOKEN and HOSTINGER_TEST_ZONE to run integration tests")
	}
	return &hostinger.Provider{APIToken: token}, zone
}

func TestGetRecords(t *testing.T) {
	provider, zone := newProvider(t)

	records, err := provider.GetRecords(context.Background(), zone)
	if err != nil {
		t.Fatalf("GetRecords: %v", err)
	}
	t.Logf("Got %d records", len(records))
	for _, rec := range records {
		t.Logf("  %s", rec.RR())
	}
}

func TestAppendAndDeleteRecords(t *testing.T) {
	provider, zone := newProvider(t)
	ctx := context.Background()

	testRecord := libdns.Address{
		Name: "_libdns-test-append",
		TTL:  300 * time.Second,
		IP:   netip.MustParseAddr("198.51.100.1"),
	}

	// Append
	appended, err := provider.AppendRecords(ctx, zone, []libdns.Record{testRecord})
	if err != nil {
		t.Fatalf("AppendRecords: %v", err)
	}
	if len(appended) != 1 {
		t.Fatalf("expected 1 appended record, got %d", len(appended))
	}

	// Verify it exists
	records, err := provider.GetRecords(ctx, zone)
	if err != nil {
		t.Fatalf("GetRecords after append: %v", err)
	}
	found := false
	for _, rec := range records {
		rr := rec.RR()
		if rr.Name == "_libdns-test-append" && rr.Type == "A" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("appended record not found in zone")
	}

	// Delete
	deleted, err := provider.DeleteRecords(ctx, zone, []libdns.Record{testRecord})
	if err != nil {
		t.Fatalf("DeleteRecords: %v", err)
	}
	if len(deleted) != 1 {
		t.Fatalf("expected 1 deleted record, got %d", len(deleted))
	}
}

func TestSetRecords(t *testing.T) {
	provider, zone := newProvider(t)
	ctx := context.Background()

	testRecord := libdns.Address{
		Name: "_libdns-test-set",
		TTL:  300 * time.Second,
		IP:   netip.MustParseAddr("198.51.100.2"),
	}

	// Set (create)
	set, err := provider.SetRecords(ctx, zone, []libdns.Record{testRecord})
	if err != nil {
		t.Fatalf("SetRecords: %v", err)
	}
	if len(set) != 1 {
		t.Fatalf("expected 1 set record, got %d", len(set))
	}

	// Set (overwrite with new value)
	updated := libdns.Address{
		Name: "_libdns-test-set",
		TTL:  300 * time.Second,
		IP:   netip.MustParseAddr("198.51.100.3"),
	}
	set, err = provider.SetRecords(ctx, zone, []libdns.Record{updated})
	if err != nil {
		t.Fatalf("SetRecords (overwrite): %v", err)
	}
	if len(set) != 1 {
		t.Fatalf("expected 1 set record, got %d", len(set))
	}

	// Cleanup
	_, err = provider.DeleteRecords(ctx, zone, []libdns.Record{updated})
	if err != nil {
		t.Fatalf("DeleteRecords (cleanup): %v", err)
	}
}

// TestAppendAndDeleteTXTRecords tests TXT record creation and deletion,
// which exercises the same code path as ACME DNS-01 challenge cleanup.
func TestAppendAndDeleteTXTRecords(t *testing.T) {
	provider, zone := newProvider(t)
	ctx := context.Background()

	testRecord := libdns.TXT{
		Name: "_libdns-test-txt",
		TTL:  300 * time.Second,
		Text: "test-challenge-token",
	}

	// Append
	appended, err := provider.AppendRecords(ctx, zone, []libdns.Record{testRecord})
	if err != nil {
		t.Fatalf("AppendRecords: %v", err)
	}
	if len(appended) != 1 {
		t.Fatalf("expected 1 appended record, got %d", len(appended))
	}

	// Verify it exists and content is unquoted
	records, err := provider.GetRecords(ctx, zone)
	if err != nil {
		t.Fatalf("GetRecords after append: %v", err)
	}
	found := false
	for _, rec := range records {
		rr := rec.RR()
		if rr.Name == "_libdns-test-txt" && rr.Type == "TXT" {
			if rr.Data != "test-challenge-token" {
				t.Errorf("TXT content = %q, want %q", rr.Data, "test-challenge-token")
			}
			found = true
			break
		}
	}
	if !found {
		t.Fatal("appended TXT record not found in zone")
	}

	// Delete using the same record (simulates certmagic CleanUp)
	deleted, err := provider.DeleteRecords(ctx, zone, []libdns.Record{testRecord})
	if err != nil {
		t.Fatalf("DeleteRecords: %v", err)
	}
	if len(deleted) != 1 {
		t.Fatalf("expected 1 deleted record, got %d", len(deleted))
	}

	// Verify it's gone
	records, err = provider.GetRecords(ctx, zone)
	if err != nil {
		t.Fatalf("GetRecords after delete: %v", err)
	}
	for _, rec := range records {
		rr := rec.RR()
		if rr.Name == "_libdns-test-txt" && rr.Type == "TXT" {
			t.Fatal("TXT record still exists after deletion")
		}
	}
}
