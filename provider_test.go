package hostinger_test

import (
	"os"
	"testing"

	"github.com/libdns/hostinger"
	"github.com/libdns/libdns/libdnstest"
)

func TestProvider(t *testing.T) {
	token := os.Getenv("HOSTINGER_API_TOKEN")
	zone := os.Getenv("HOSTINGER_TEST_ZONE")
	if token == "" || zone == "" {
		t.Skip("Set HOSTINGER_API_TOKEN and HOSTINGER_TEST_ZONE to run integration tests")
	}

	provider := &hostinger.Provider{APIToken: token}

	suite := libdnstest.NewTestSuite(libdnstest.WrapNoZoneLister(provider), zone)
	suite.SkipRRTypes = map[string]bool{
		"SVCB":  true,
		"HTTPS": true,
	}
	suite.RunTests(t)
}
