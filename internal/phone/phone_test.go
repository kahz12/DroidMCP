package phone

import "testing"

func TestParseRecipients(t *testing.T) {
	joined, nums, err := ParseRecipients("+1555, 800900 ,+1555")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if joined != "+1555,800900,+1555" || len(nums) != 3 {
		t.Errorf("got %q %v", joined, nums)
	}
	for _, bad := range []string{"", "  ", "ab", "12", "+", "1 2 3", "-n", "123;456", "+34 600"} {
		if _, _, err := ParseRecipients(bad); err == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
}
