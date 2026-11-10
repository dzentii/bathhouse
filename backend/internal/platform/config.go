package platform

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Service, Addr, DatabaseURL, RedisURL, KafkaAddr, Topic, InternalKey                            string
	IdentityURL, BookingURL, PaymentURL, CatalogURL, OrderKey, TicketKey, AdminUser, AdminPassword string
	PublicURL, AllowedOrigin, Env                                                                  string
	SecureCookie                                                                                   bool
	TrustedProxyHosts                                                                              []string
	TrustedProxyCIDRs                                                                              []string
	HTTPMaxConcurrent, PublicRatePerMinute, WriteRatePerMinute, HeavyRatePerMinute                 int
	MaxActiveHoldsPerClient, MaxHeldResourcesPerClient, PDFMaxConcurrent, PDFCacheMiB              int
	OutboxRetentionDays, InboxRetentionDays                                                        int
}

func Env(key, fallback string) string {
	if s := os.Getenv(key); s != "" {
		return s
	}
	return fallback
}

func Load(service string) (Config, error) {
	c := Config{Service: service, Addr: Env("HTTP_ADDR", ":8080"), DatabaseURL: os.Getenv("DATABASE_URL"), RedisURL: Env("REDIS_URL", "redis://localhost:6379/0"), KafkaAddr: Env("KAFKA_ADDR", "localhost:9092"), Topic: Env("KAFKA_TOPIC", "banya.events.v1"), InternalKey: os.Getenv("INTERNAL_API_KEY"), IdentityURL: Env("IDENTITY_URL", "http://identity:8080"), BookingURL: Env("BOOKING_URL", "http://booking:8080"), PaymentURL: Env("PAYMENT_URL", "http://payment:8080"), CatalogURL: Env("CATALOG_URL", "http://catalog:8080"), OrderKey: os.Getenv("ORDER_TOKEN_KEY"), TicketKey: os.Getenv("TICKET_TOKEN_KEY"), AdminUser: os.Getenv("ADMIN_USERNAME"), AdminPassword: os.Getenv("ADMIN_PASSWORD"), PublicURL: strings.TrimRight(Env("PUBLIC_URL", "http://localhost:8080"), "/"), AllowedOrigin: os.Getenv("ALLOWED_ORIGIN"), Env: Env("APP_ENV", "development")}
	var parseErr error
	for _, field := range []struct {
		key               string
		target            *int
		fallback, maximum int
	}{
		{"HTTP_MAX_CONCURRENT", &c.HTTPMaxConcurrent, 64, 1024},
		{"PUBLIC_RATE_PER_MINUTE", &c.PublicRatePerMinute, 1200, 100000},
		{"WRITE_RATE_PER_MINUTE", &c.WriteRatePerMinute, 300, 10000},
		{"HEAVY_RATE_PER_MINUTE", &c.HeavyRatePerMinute, 60, 10000},
		{"MAX_ACTIVE_HOLDS_PER_CLIENT", &c.MaxActiveHoldsPerClient, 5, 100},
		{"MAX_HELD_RESOURCES_PER_CLIENT", &c.MaxHeldResourcesPerClient, 100, 1000},
		{"PDF_MAX_CONCURRENT", &c.PDFMaxConcurrent, 2, 16},
		{"PDF_CACHE_MIB", &c.PDFCacheMiB, 16, 256},
		{"OUTBOX_RETENTION_DAYS", &c.OutboxRetentionDays, 30, 3650},
		{"INBOX_RETENTION_DAYS", &c.InboxRetentionDays, 90, 3650},
	} {
		n, err := strconv.Atoi(Env(field.key, strconv.Itoa(field.fallback)))
		if err != nil || n < 1 || n > field.maximum {
			return c, fmt.Errorf("invalid %s", field.key)
		}
		*field.target = n
	}
	if c.InboxRetentionDays < c.OutboxRetentionDays+7 {
		return c, errors.New("INBOX_RETENTION_DAYS must exceed OUTBOX_RETENTION_DAYS by at least 7 days")
	}
	for _, item := range []struct {
		key    string
		target *[]string
	}{{"TRUSTED_PROXY_HOSTS", &c.TrustedProxyHosts}, {"TRUSTED_PROXY_CIDRS", &c.TrustedProxyCIDRs}} {
		if value := os.Getenv(item.key); value != "" {
			*item.target = strings.Split(value, ",")
		}
	}
	if err := validateProxyConfig(c); err != nil {
		return c, err
	}
	c.SecureCookie, parseErr = strconv.ParseBool(Env("COOKIE_SECURE", "false"))
	if parseErr != nil {
		return c, errors.New("COOKIE_SECURE must be true or false")
	}
	if c.Env != "development" && c.Env != "production" && c.Env != "test" {
		return c, errors.New("APP_ENV must be development, test or production")
	}
	for _, value := range []string{c.PublicURL, c.IdentityURL, c.BookingURL, c.PaymentURL, c.CatalogURL} {
		u, err := url.Parse(value)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
			return c, errors.New("service URLs must be HTTP(S) origins without credentials, query or path")
		}
	}
	if c.DatabaseURL == "" || len(c.InternalKey) < 32 {
		return c, errors.New("DATABASE_URL and INTERNAL_API_KEY (at least 32 characters) are required")
	}
	if service == "booking" && (len(c.OrderKey) < 32 || len(c.TicketKey) < 32 || c.OrderKey == c.TicketKey) {
		return c, errors.New("distinct ORDER_TOKEN_KEY and TICKET_TOKEN_KEY of at least 32 characters are required")
	}
	if service == "identity" && (len(c.AdminUser) < 3 || len(c.AdminPassword) < 12 || len(c.AdminPassword) > 72) {
		return c, errors.New("ADMIN_USERNAME and ADMIN_PASSWORD (12..72 bytes) are required")
	}
	if c.Env == "production" && (!c.SecureCookie || !strings.HasPrefix(c.PublicURL, "https://")) {
		return c, fmt.Errorf("production requires COOKIE_SECURE=true and an HTTPS PUBLIC_URL")
	}
	return c, nil
}
