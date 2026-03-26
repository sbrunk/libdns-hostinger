// Package hostinger implements a DNS record management client compatible
// with the libdns interfaces for Hostinger.
package hostinger

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/libdns/libdns"
)

// Provider facilitates DNS record manipulation with Hostinger.
type Provider struct {
	// APIToken is the Hostinger API token used for authentication.
	// Tokens can be managed at https://hpanel.hostinger.com/profile/api.
	// If empty, defaults to the HOSTINGER_API_TOKEN environment variable.
	APIToken string `json:"api_token,omitempty"`

	httpClient *http.Client
	once       sync.Once
	mu         sync.Mutex
}

// GetRecords lists all the records in the zone.
func (p *Provider) GetRecords(ctx context.Context, zone string) ([]libdns.Record, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	domain := unFQDN(zone)

	respBody, err := p.doRequest(ctx, http.MethodGet, "/api/dns/v1/zones/"+domain, nil)
	if err != nil {
		return nil, fmt.Errorf("getting records for zone %s: %w", zone, err)
	}

	var zoneRecords []dnsZoneRecord
	if err := json.Unmarshal(respBody, &zoneRecords); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}

	var records []libdns.Record
	for _, zr := range zoneRecords {
		ttl := time.Duration(zr.TTL) * time.Second
		for _, rc := range zr.Records {
			rr := libdns.RR{
				Name: zr.Name,
				Type: zr.Type,
				TTL:  ttl,
				Data: rc.Content,
			}
			parsed, _ := rr.Parse()
			records = append(records, parsed)
		}
	}

	return records, nil
}

// AppendRecords creates the inputted records in the given zone and returns
// the populated records that were created. It never changes existing records.
func (p *Provider) AppendRecords(ctx context.Context, zone string, records []libdns.Record) ([]libdns.Record, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	domain := unFQDN(zone)
	zoneEntries := groupRecords(records)

	req := updateRequest{
		Zone:      zoneEntries,
		Overwrite: false,
	}

	_, err := p.doRequest(ctx, http.MethodPut, "/api/dns/v1/zones/"+domain, req)
	if err != nil {
		return nil, fmt.Errorf("appending records to zone %s: %w", zone, err)
	}

	return toConcrete(records), nil
}

// SetRecords sets the records in the zone, either by updating existing records
// or creating new ones. For each (name, type) pair in the input, all existing
// records with that pair are replaced with the provided records. It returns the
// records that were set.
func (p *Provider) SetRecords(ctx context.Context, zone string, records []libdns.Record) ([]libdns.Record, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	domain := unFQDN(zone)
	zoneEntries := groupRecords(records)

	req := updateRequest{
		Zone:      zoneEntries,
		Overwrite: true,
	}

	_, err := p.doRequest(ctx, http.MethodPut, "/api/dns/v1/zones/"+domain, req)
	if err != nil {
		return nil, fmt.Errorf("setting records in zone %s: %w", zone, err)
	}

	return toConcrete(records), nil
}

// DeleteRecords deletes the specified records from the zone. It returns the
// records that were deleted. Records that do not exist in the zone are silently
// ignored.
//
// Because the Hostinger API only supports deleting all records matching a
// (name, type) pair, this method performs a read-modify-write: it reads the
// current zone, determines which records to keep, and either deletes the entire
// record set or overwrites it with the remaining records.
func (p *Provider) DeleteRecords(ctx context.Context, zone string, records []libdns.Record) ([]libdns.Record, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	domain := unFQDN(zone)

	// Fetch current records to match against and handle selective deletion.
	respBody, err := p.doRequest(ctx, http.MethodGet, "/api/dns/v1/zones/"+domain, nil)
	if err != nil {
		return nil, fmt.Errorf("getting current records for zone %s: %w", zone, err)
	}

	var currentZone []dnsZoneRecord
	if err := json.Unmarshal(respBody, &currentZone); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}

	// Build lookup of current records by (name, type).
	type rrsetKey struct{ Name, Type string }
	currentByKey := make(map[rrsetKey]dnsZoneRecord)
	for _, zr := range currentZone {
		key := rrsetKey{zr.Name, zr.Type}
		currentByKey[key] = zr
	}

	// For each affected (name, type), track content values to keep vs delete.
	type rrsetAction struct {
		toKeep  []recordContent
		deleted []libdns.Record
		ttl     int
	}
	actions := make(map[rrsetKey]*rrsetAction)

	for _, rec := range records {
		rr := rec.RR()
		if rr.Name == "" {
			continue
		}

		for key, current := range currentByKey {
			// Name must match.
			if key.Name != rr.Name {
				continue
			}
			// Type: if input type is empty, match any; otherwise must match.
			if rr.Type != "" && key.Type != rr.Type {
				continue
			}

			if actions[key] == nil {
				actions[key] = &rrsetAction{
					toKeep: append([]recordContent{}, current.Records...),
					ttl:    current.TTL,
				}
			}
			act := actions[key]

			ttlDur := time.Duration(current.TTL) * time.Second

			var remaining []recordContent
			for _, rc := range act.toKeep {
				dataMatch := rr.Data == "" || rc.Content == rr.Data
				ttlMatch := rr.TTL == 0 || ttlDur == rr.TTL

				if dataMatch && ttlMatch {
					delRR := libdns.RR{
						Name: key.Name,
						Type: key.Type,
						TTL:  ttlDur,
						Data: rc.Content,
					}
					parsed, _ := delRR.Parse()
					act.deleted = append(act.deleted, parsed)
				} else {
					remaining = append(remaining, rc)
				}
			}
			act.toKeep = remaining
		}
	}

	// Build API requests for the two possible actions:
	// 1. DELETE filter for (name, type) pairs being fully removed.
	// 2. PUT overwrite=true for (name, type) pairs being partially updated.
	var deleteFilters []deleteFilter
	var updateEntries []dnsZoneRecord
	var allDeleted []libdns.Record

	for key, act := range actions {
		if len(act.deleted) == 0 {
			continue
		}
		allDeleted = append(allDeleted, act.deleted...)

		if len(act.toKeep) == 0 {
			deleteFilters = append(deleteFilters, deleteFilter{
				Name: key.Name,
				Type: key.Type,
			})
		} else {
			updateEntries = append(updateEntries, dnsZoneRecord{
				Name:    key.Name,
				Type:    key.Type,
				TTL:     act.ttl,
				Records: act.toKeep,
			})
		}
	}

	if len(deleteFilters) > 0 {
		_, err := p.doRequest(ctx, http.MethodDelete, "/api/dns/v1/zones/"+domain, deleteRequest{Filters: deleteFilters})
		if err != nil {
			return nil, fmt.Errorf("deleting records from zone %s: %w", zone, err)
		}
	}

	if len(updateEntries) > 0 {
		_, err := p.doRequest(ctx, http.MethodPut, "/api/dns/v1/zones/"+domain, updateRequest{Zone: updateEntries, Overwrite: true})
		if err != nil {
			return nil, fmt.Errorf("updating remaining records in zone %s: %w", zone, err)
		}
	}

	return allDeleted, nil
}

// toConcrete ensures all records are concrete types (Address, TXT, etc.)
// rather than opaque RR structs.
func toConcrete(records []libdns.Record) []libdns.Record {
	result := make([]libdns.Record, len(records))
	for i, rec := range records {
		parsed, _ := rec.RR().Parse()
		result[i] = parsed
	}
	return result
}

// groupRecords groups libdns records by (name, type) into the Hostinger
// zone record format. When multiple records share the same (name, type),
// the minimum TTL is used for the group.
func groupRecords(records []libdns.Record) []dnsZoneRecord {
	type rrsetKey struct{ Name, Type string }
	groups := make(map[rrsetKey]*dnsZoneRecord)
	var order []rrsetKey

	for _, rec := range records {
		rr := rec.RR()
		name := rr.Name
		if name == "" {
			name = "@"
		}

		key := rrsetKey{name, rr.Type}
		if groups[key] == nil {
			groups[key] = &dnsZoneRecord{
				Name: name,
				Type: rr.Type,
				TTL:  int(rr.TTL / time.Second),
			}
			order = append(order, key)
		}
		groups[key].Records = append(groups[key].Records, recordContent{Content: rr.Data})

		if ttl := int(rr.TTL / time.Second); ttl < groups[key].TTL {
			groups[key].TTL = ttl
		}
	}

	entries := make([]dnsZoneRecord, 0, len(order))
	for _, key := range order {
		entries = append(entries, *groups[key])
	}
	return entries
}

// unFQDN removes the trailing dot from a fully-qualified domain name.
func unFQDN(zone string) string {
	return strings.TrimSuffix(zone, ".")
}

// Interface guards
var (
	_ libdns.RecordGetter   = (*Provider)(nil)
	_ libdns.RecordAppender = (*Provider)(nil)
	_ libdns.RecordSetter   = (*Provider)(nil)
	_ libdns.RecordDeleter  = (*Provider)(nil)
)
