package scope

import "testing"

func TestLoopback(t *testing.T) {
	cases := []struct {
		allow []string
		want  bool
	}{
		{[]string{"127.0.0.1", "localhost"}, true},
		{[]string{"localhost"}, true},
		{[]string{"::1"}, true},
		{[]string{"app.localhost"}, true},
		{[]string{"127.0.0.1", "example.com"}, false},
		{[]string{"192.168.1.10"}, false},
		{[]string{"8.8.8.8"}, false},
		{[]string{}, false},
	}
	for _, c := range cases {
		if got := Loopback(c.allow); got != c.want {
			t.Errorf("Loopback(%v) = %v, want %v", c.allow, got, c.want)
		}
	}
}

func TestValidate(t *testing.T) {
	desc, err := Validate([]string{"127.0.0.1", "localhost"}, []int{8080}, []string{"*"})
	if err != nil {
		t.Fatal(err)
	}
	want := "allow=127.0.0.1,localhost ports=8080 deny=*"
	if desc != want {
		t.Fatalf("desc = %q, want %q", desc, want)
	}

	if _, err := Validate([]string{"example.com"}, []int{8080}, []string{"*"}); err == nil {
		t.Fatal("expected error for non-loopback allow")
	}
	if _, err := Validate(nil, nil, nil); err == nil {
		t.Fatal("expected error for empty allow")
	}
}
