package main

import "testing"

func TestNativeWindowFollowsAppearancePreference(t *testing.T) {
	for _, systemDark := range []bool{false, true} {
		for _, preference := range []string{"", "light", "dark"} {
			want := preference == "dark" || preference == "" && systemDark
			if got := nativeWindowDark(preference, systemDark, false); got != want {
				t.Errorf("preference=%q systemDark=%v: got %v, want %v", preference, systemDark, got, want)
			}
			if nativeWindowDark(preference, systemDark, true) {
				t.Errorf("preference=%q must not override Windows high contrast", preference)
			}
		}
	}
}
