package module

import (
	"reflect"
	"testing"
)

func TestComponentPinLifecycle(t *testing.T) {
	marker := componentPinMarker{
		Schema:    componentPinMarkerSchema,
		Component: "oh-my-zsh-base",
		Source:    "https://github.com/ohmyzsh/ohmyzsh.git",
		Commit:    "146461f7c6d95f4ba1220559d66eb113418b40a8",
		Owned:     ohMyZshOwnedEntries(),
		Files:     map[string]string{"oh-my-zsh.sh": "digest"},
	}
	if err := marker.validate(); err != nil {
		t.Fatalf("valid marker rejected: %v", err)
	}
	if !reflect.DeepEqual(marker.Owned, expectedOhMyZshOwnedEntries) {
		t.Fatalf("owned entries = %#v, want %#v", marker.Owned, expectedOhMyZshOwnedEntries)
	}
}
