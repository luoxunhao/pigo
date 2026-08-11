package run

import "testing"

func TestWireModel(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{in: "openai/agnes-2.5-flash", want: "agnes-2.5-flash"},
		{in: "opencode-go/deepseek-v4-flash", want: "deepseek-v4-flash"},
		{in: "agnes-2.5-flash", want: "agnes-2.5-flash"},
		{in: "", want: ""},
		{in: "/x", want: "/x"},
	}
	for _, c := range cases {
		if got := WireModel(c.in); got != c.want {
			t.Errorf("WireModel(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
