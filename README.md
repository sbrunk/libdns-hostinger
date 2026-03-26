# Hostinger for [`libdns`](https://github.com/libdns/libdns)

[![Go Reference](https://pkg.go.dev/badge/github.com/libdns/hostinger.svg)](https://pkg.go.dev/github.com/libdns/hostinger)

This package implements the [libdns interfaces](https://github.com/libdns/libdns) for [Hostinger](https://www.hostinger.com/), allowing you to manage DNS records.

## Configuration

The provider requires a Hostinger API token. You can create and manage tokens from the [Hostinger Panel](https://hpanel.hostinger.com/profile/api).

```go
provider := &hostinger.Provider{
    APIToken: "YOUR_API_TOKEN",
}
```

Alternatively, set the `HOSTINGER_API_TOKEN` environment variable as a fallback.

## Usage

```go
package main

import (
    "context"
    "fmt"
    "net/netip"
    "time"

    "github.com/libdns/hostinger"
    "github.com/libdns/libdns"
)

func main() {
    provider := &hostinger.Provider{
        APIToken: "YOUR_API_TOKEN",
    }

    // List all records in the zone
    records, err := provider.GetRecords(context.Background(), "example.com.")
    if err != nil {
        panic(err)
    }
    for _, rec := range records {
        fmt.Println(rec.RR())
    }

    // Add a new A record
    _, err = provider.AppendRecords(context.Background(), "example.com.", []libdns.Record{
        libdns.Address{
            Name: "test",
            TTL:  300 * time.Second,
            IP:   netip.MustParseAddr("1.2.3.4"),
        },
    })
    if err != nil {
        panic(err)
    }
}
```

## Testing

To run integration tests, set the following environment variables:

```bash
HOSTINGER_API_TOKEN=your_api_token \
HOSTINGER_TEST_ZONE=your-test-domain.com. \
go test -v
```

**Warning:** Integration tests create and delete real DNS records. Use a dedicated test zone.

## Notes

- The Hostinger API does not support listing available zones, so the `ZoneLister` interface is not implemented.
- `DeleteRecords` performs a read-modify-write operation because the Hostinger API only supports deleting all records matching a `(name, type)` pair. To remove individual record values, the provider overwrites the record set with the remaining values.
- Basic retry logic (3 attempts with exponential backoff) is applied for transient HTTP errors (429, 5xx).
- All methods are safe for concurrent use.
