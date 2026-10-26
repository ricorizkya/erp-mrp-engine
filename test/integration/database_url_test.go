//go:build integration

package integration_test

import "testing"

func TestValidateTestDatabaseURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		dsn     string
		wantErr bool
	}{
		{
			name: "isolated test database",
			dsn:  "postgres://user:password@localhost:55432/erp_test?sslmode=disable",
		},
		{
			name:    "development database",
			dsn:     "postgres://user:password@localhost:5432/erp_development",
			wantErr: true,
		},
		{
			name:    "invalid URL",
			dsn:     "not-a-url",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateTestDatabaseURL(tt.dsn)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateTestDatabaseURL() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
