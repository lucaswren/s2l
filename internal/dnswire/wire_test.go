package dnswire

import "testing"

func TestResponseQuestionMatchesASCIICaseOnly(t *testing.T) {
	for _, tc := range []struct {
		query, response string
		wantMatch       bool
	}{
		{"ExAmPlE.com", "example.COM", true},
		{"Ä.example", "ä.example", false},
	} {
		query, err := QueryA(tc.query)
		if err != nil {
			t.Fatal(err)
		}
		reply, err := QueryA(tc.response)
		if err != nil {
			t.Fatal(err)
		}
		copy(reply[:2], query[:2])
		reply[2] |= 0x80
		if match := ValidateResponse(query, reply) == nil; match != tc.wantMatch {
			t.Errorf("matching %q to %q = %v, want %v", tc.query, tc.response, match, tc.wantMatch)
		}
	}
}
