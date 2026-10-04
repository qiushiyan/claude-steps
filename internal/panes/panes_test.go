package panes

import (
	"reflect"
	"testing"
)

func TestParseKeepsPanesWithASession(t *testing.T) {
	out := "%1\twork:1.1\t11111111-1111-4111-8111-111111111111\n%2\twork:1.2\t\n%3\tmy notes:2.1\t22222222-2222-4222-8222-222222222222\nbroken line\n"
	want := []Pane{
		{ID: "%1", Where: "work:1.1", SessionID: "11111111-1111-4111-8111-111111111111"},
		{ID: "%3", Where: "my notes:2.1", SessionID: "22222222-2222-4222-8222-222222222222"},
	}
	if got := parse(out); !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v", got)
	}
}
