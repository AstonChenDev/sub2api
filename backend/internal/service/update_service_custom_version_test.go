package service

import "testing"

func TestCompareVersionsUnderstandsCustomBuildVersion(t *testing.T) {
	tests := []struct {
		name    string
		current string
		latest  string
		want    int
	}{
		{name: "same upstream version", current: "custom-v0.2.3-34b1ccca658c", latest: "0.2.3", want: 0},
		{name: "upstream update available", current: "custom-v0.2.3-34b1ccca658c", latest: "0.2.4", want: -1},
		{name: "custom build ahead", current: "custom-v0.3.0-34b1ccca658c", latest: "0.2.4", want: 1},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := compareVersions(test.current, test.latest); got != test.want {
				t.Fatalf("compareVersions(%q, %q) = %d, want %d", test.current, test.latest, got, test.want)
			}
		})
	}
}
