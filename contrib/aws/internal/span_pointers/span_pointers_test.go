package span_pointers

import (
	"testing"
)

func TestGeneratePointerHash(t *testing.T) {
	tests := []struct {
		name         string
		components   []string
		expectedHash string
	}{
		{
			name: "basic values",
			components: []string{
				"some-bucket",
				"some-key.data",
				"ab12ef34",
			},
			expectedHash: "e721375466d4116ab551213fdea08413",
		},
		{
			name: "non-ascii key",
			components: []string{
				"some-bucket",
				"some-key.你好",
				"ab12ef34",
			},
			expectedHash: "d1333a04b9928ab462b5c6cadfa401f4",
		},
		{
			name: "multipart-upload",
			components: []string{
				"some-bucket",
				"some-key.data",
				"ab12ef34-5",
			},
			expectedHash: "2b90dffc37ebc7bc610152c3dc72af9f",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := generatePointerHash(tt.components)
			if got != tt.expectedHash {
				t.Errorf("GeneratePointerHash() = %v, want %v", got, tt.expectedHash)
			}
		})
	}
}
