package db

import "testing"

func TestBundledAppManifestParses(t *testing.T) {
	paths, err := bundledAppManifestPaths()
	if err != nil {
		t.Fatalf("bundledAppManifestPaths() error = %v", err)
	}
	if len(paths) != 0 {
		t.Fatalf("expected no bundled apps, got %v", paths)
	}
}

func TestParseBundledAppRegistryValue(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    []string
		wantOK  bool
		wantErr bool
	}{
		{name: "empty", raw: "", wantOK: true},
		{name: "json array", raw: `["devworks","opsworks"]`, want: []string{"devworks", "opsworks"}, wantOK: true},
		{name: "identifier list", raw: "devworks, opsworks", want: []string{"devworks", "opsworks"}, wantOK: true},
		{name: "invalid legacy garbage", raw: "Velm Draft", wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok, err := parseBundledAppRegistryValue(tt.raw)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr = %t", err, tt.wantErr)
			}
			if ok != tt.wantOK {
				t.Fatalf("ok = %t, want %t", ok, tt.wantOK)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("len(got) = %d, want %d (%v)", len(got), len(tt.want), got)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("got[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestSameBundledAppNameSet(t *testing.T) {
	tests := []struct {
		name  string
		left  []string
		right []string
		want  bool
	}{
		{name: "same order", left: []string{"velm"}, right: []string{"velm"}, want: true},
		{name: "different order and case", left: []string{"Velm", "opsworks"}, right: []string{"opsworks", "velm"}, want: true},
		{name: "deduplicated", left: []string{"velm", "velm"}, right: []string{"velm"}, want: true},
		{name: "different names", left: []string{"velm"}, right: []string{"devworks"}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sameBundledAppNameSet(tt.left, tt.right); got != tt.want {
				t.Fatalf("sameBundledAppNameSet(%v, %v) = %t, want %t", tt.left, tt.right, got, tt.want)
			}
		})
	}
}
