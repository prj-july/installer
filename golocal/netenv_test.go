package main

import (
	"reflect"
	"testing"
)

func TestParseNameservers(t *testing.T) {
	text := "# generated\nnameserver 192.168.1.1\nnameserver 8.8.8.8\nnameserver ::1\nsearch lan\n"
	got := parseNameservers(text)
	want := []string{"192.168.1.1", "8.8.8.8"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseNameservers = %v, want %v", got, want)
	}
}

func TestValidIPv4(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want string
		ok   bool
	}{
		{"192.168.1.1", "192.168.1.1", true},
		{" 10.0.0.1 ", "10.0.0.1", true},
		{"::1", "", false},
		{"not-an-ip", "", false},
		{"", "", false},
	} {
		got, ok := validIPv4(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Errorf("validIPv4(%q) = %q,%v; want %q,%v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestUniqueStrings(t *testing.T) {
	got := uniqueStrings([]string{"a", "b", "a", "", "b", "c"})
	want := []string{"a", "b", "c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("uniqueStrings = %v, want %v", got, want)
	}
}
