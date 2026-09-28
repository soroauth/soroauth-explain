package explain

import "testing"

func TestFloor(t *testing.T) {
	const bogus Confidence = "certain"
	tests := []struct {
		a, b Confidence
		want Confidence
	}{
		{ConfidenceDecoded, ConfidenceDecoded, ConfidenceDecoded},
		{ConfidenceDecoded, ConfidencePartial, ConfidencePartial},
		{ConfidenceDecoded, ConfidenceOpaque, ConfidenceOpaque},
		{ConfidencePartial, ConfidenceDecoded, ConfidencePartial},
		{ConfidencePartial, ConfidencePartial, ConfidencePartial},
		{ConfidencePartial, ConfidenceOpaque, ConfidenceOpaque},
		{ConfidenceOpaque, ConfidenceDecoded, ConfidenceOpaque},
		{ConfidenceOpaque, ConfidencePartial, ConfidenceOpaque},
		{ConfidenceOpaque, ConfidenceOpaque, ConfidenceOpaque},
		// Unknown values floor to opaque, never pass through.
		{bogus, ConfidenceDecoded, ConfidenceOpaque},
		{ConfidenceDecoded, bogus, ConfidenceOpaque},
		{bogus, ConfidencePartial, ConfidenceOpaque},
		{ConfidenceOpaque, bogus, ConfidenceOpaque},
		{bogus, bogus, ConfidenceOpaque},
		{"", ConfidenceDecoded, ConfidenceOpaque},
		{ConfidenceDecoded, "", ConfidenceOpaque},
	}
	for _, tt := range tests {
		t.Run(string(tt.a)+"_"+string(tt.b), func(t *testing.T) {
			if got := Floor(tt.a, tt.b); got != tt.want {
				t.Fatalf("Floor(%q, %q) = %q, want %q", tt.a, tt.b, got, tt.want)
			}
		})
	}
}
