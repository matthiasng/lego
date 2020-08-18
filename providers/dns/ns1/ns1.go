// Package ns1 implements a DNS provider for solving the DNS-01 challenge using NS1 DNS.
package ns1

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-acme/lego/v3/challenge/dns01"
	"github.com/go-acme/lego/v3/log"
	"github.com/go-acme/lego/v3/platform/config/env"
	"gopkg.in/ns1/ns1-go.v2/rest"
	"gopkg.in/ns1/ns1-go.v2/rest/model/dns"
)

// Environment variables names.
const (
	envNamespace = "NS1_"

	EnvAPIKey = envNamespace + "API_KEY"

	EnvTTL                = envNamespace + "TTL"
	EnvPropagationTimeout = envNamespace + "PROPAGATION_TIMEOUT"
	EnvPollingInterval    = envNamespace + "POLLING_INTERVAL"
	EnvHTTPTimeout        = envNamespace + "HTTP_TIMEOUT"
)

// Config is used to configure the creation of the DNSProvider.
type Config struct {
	APIKey             string
	PropagationTimeout time.Duration
	PollingInterval    time.Duration
	TTL                int
	HTTPClient         *http.Client
}

// NewDefaultConfig returns a default configuration for the DNSProvider.
func NewDefaultConfig() *Config {
	return &Config{
		TTL:                env.GetOrDefaultInt(EnvTTL, dns01.DefaultTTL),
		PropagationTimeout: env.GetOrDefaultSecond(EnvPropagationTimeout, dns01.DefaultPropagationTimeout),
		PollingInterval:    env.GetOrDefaultSecond(EnvPollingInterval, dns01.DefaultPollingInterval),
		HTTPClient: &http.Client{
			Timeout: env.GetOrDefaultSecond(EnvHTTPTimeout, 10*time.Second),
		},
	}
}

// DNSProvider implements the challenge.Provider interface.
type DNSProvider struct {
	client *rest.Client
	config *Config
}

// NewDNSProvider returns a DNSProvider instance configured for NS1.
// Credentials must be passed in the environment variables: NS1_API_KEY.
func NewDNSProvider() (*DNSProvider, error) {
	values, err := env.Get(EnvAPIKey)
	if err != nil {
		return nil, fmt.Errorf("ns1: %w", err)
	}

	config := NewDefaultConfig()
	config.APIKey = values[EnvAPIKey]

	return NewDNSProviderConfig(config)
}

// NewDNSProviderConfig return a DNSProvider instance configured for NS1.
func NewDNSProviderConfig(config *Config) (*DNSProvider, error) {
	if config == nil {
		return nil, errors.New("ns1: the configuration of the DNS provider is nil")
	}

	if config.APIKey == "" {
		return nil, errors.New("ns1: credentials missing")
	}

	client := rest.NewClient(config.HTTPClient, rest.SetAPIKey(config.APIKey))

	return &DNSProvider{client: client, config: config}, nil
}

// Present creates a TXT record to fulfill the DNS-01 challenge.
func (d *DNSProvider) Present(domain, token, keyAuth string) error {
	fqdn, value := dns01.GetRecord(domain, keyAuth)
	return d.CreateRecord(domain, token, fqdn, value)
}

// Present creates a TXT record to fulfill the DNS-01 challenge.
func (d *DNSProvider) CreateRecord(domain, token, fqdn, value string) error {
	zone, err := d.getHostedZone(fqdn)
	if err != nil {
		return fmt.Errorf("ns1: %w", err)
	}

	record, _, err := d.client.Records.Get(zone.Zone, dns01.UnFqdn(fqdn), "TXT")

	// Create a new record
	if err == rest.ErrRecordMissing || record == nil {
		log.Infof("Create a new record for [zone: %s, fqdn: %s, domain: %s]", zone.Zone, fqdn, domain)

		record = dns.NewRecord(zone.Zone, dns01.UnFqdn(fqdn), "TXT")
		record.TTL = d.config.TTL
		record.Answers = []*dns.Answer{{Rdata: []string{value}}}

		_, err = d.client.Records.Create(record)
		if err != nil {
			return fmt.Errorf("ns1: failed to create record [zone: %q, fqdn: %q]: %w", zone.Zone, fqdn, err)
		}

		return nil
	}

	if err != nil {
		return fmt.Errorf("ns1: failed to get the existing record: %w", err)
	}

	// Update the existing records
	record.Answers = append(record.Answers, &dns.Answer{Rdata: []string{value}})

	log.Infof("Update an existing record for [zone: %s, fqdn: %s, domain: %s]", zone.Zone, fqdn, domain)

	_, err = d.client.Records.Update(record)
	if err != nil {
		return fmt.Errorf("ns1: failed to update record [zone: %q, fqdn: %q]: %w", zone.Zone, fqdn, err)
	}

	return nil
}

// CleanUp removes the TXT record matching the specified parameters.
func (d *DNSProvider) CleanUp(domain, token, keyAuth string) error {
	fqdn, value := dns01.GetRecord(domain, keyAuth)
	return d.DeleteRecord(domain, token, fqdn, value)
}

// DeleteRecord removes the record matching the specified parameters.
func (d *DNSProvider) DeleteRecord(domain, token, fqdn, value string) error {
	zone, err := d.getHostedZone(fqdn)
	if err != nil {
		return fmt.Errorf("ns1: %w", err)
	}

	name := dns01.UnFqdn(fqdn)
	_, err = d.client.Records.Delete(zone.Zone, name, "TXT")
	if err != nil {
		return fmt.Errorf("ns1: failed to delete record [zone: %q, domain: %q]: %w", zone.Zone, name, err)
	}
	return nil
}

// Timeout returns the timeout and interval to use when checking for DNS propagation.
// Adjusting here to cope with spikes in propagation times.
func (d *DNSProvider) Timeout() (timeout, interval time.Duration) {
	return d.config.PropagationTimeout, d.config.PollingInterval
}

func (d *DNSProvider) getHostedZone(fqdn string) (*dns.Zone, error) {
	authZone, err := getAuthZone(fqdn)
	if err != nil {
		return nil, fmt.Errorf("failed to extract auth zone from fqdn %q: %w", fqdn, err)
	}

	zone, _, err := d.client.Zones.Get(authZone)
	if err != nil {
		return nil, fmt.Errorf("failed to get zone [authZone: %q, fqdn: %q]: %w", authZone, fqdn, err)
	}

	return zone, nil
}

func getAuthZone(fqdn string) (string, error) {
	authZone, err := dns01.FindZoneByFqdn(fqdn)
	if err != nil {
		return "", err
	}

	return strings.TrimSuffix(authZone, "."), nil
}
