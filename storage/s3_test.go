package storage

import "testing"

func TestNewS3RejectsIncompleteConfig(t *testing.T) {
	valid := Config{
		Driver:    "s3",
		Endpoint:  "127.0.0.1:9000",
		Bucket:    "files",
		AccessKey: "key",
		SecretKey: "secret",
	}

	cases := []struct {
		name   string
		mutate func(*Config)
	}{
		{"missing endpoint", func(c *Config) { c.Endpoint = "" }},
		{"missing bucket", func(c *Config) { c.Bucket = "" }},
		{"missing access key", func(c *Config) { c.AccessKey = "" }},
		{"missing secret key", func(c *Config) { c.SecretKey = "" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := valid
			tc.mutate(&cfg)
			if _, err := NewS3(cfg); err == nil {
				t.Fatal("expected config error")
			}
			if _, err := New(cfg); err == nil {
				t.Fatal("expected New to reject incomplete config")
			}
		})
	}
}

func TestNewS3CreatesClientWithoutNetwork(t *testing.T) {
	cfg := Config{
		Driver:    "s3",
		Endpoint:  "http://127.0.0.1:9000",
		Bucket:    "files",
		Region:    "us-east-1",
		AccessKey: "key",
		SecretKey: "secret",
	}

	store, err := NewS3(cfg)
	if err != nil {
		t.Fatalf("NewS3: %v", err)
	}
	if store == nil {
		t.Fatal("expected storage instance")
	}

	dispatched, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if dispatched == nil {
		t.Fatal("expected dispatched storage instance")
	}

	cfg.Driver = "minio"
	if _, err := New(cfg); err != nil {
		t.Fatalf("New minio: %v", err)
	}
}
