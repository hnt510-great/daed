package config

import (
	"reflect"
	"strings"
	"testing"

	"github.com/daeuniverse/dae-wing/graphql/service/config/global"
)

func TestApplyDefaultLanInterface(t *testing.T) {
	for _, tc := range []struct {
		env   string
		input *[]string
		want  []string
	}{
		{"br-lan", nil, []string{"br-lan"}},
		{"br-lan", &[]string{}, []string{"br-lan"}},
		{"br-lan, br-guest", nil, []string{"br-lan", "br-guest"}},
		{"br-lan", &[]string{"eth0"}, []string{"eth0"}},
		{"", nil, nil},
		{" , ", &[]string{}, []string{}},
	} {
		t.Setenv(DefaultLanInterfaceEnv, tc.env)
		glob := &global.Input{LanInterface: tc.input}
		applyDefaultLanInterface(glob)
		var got []string
		if glob.LanInterface != nil {
			got = *glob.LanInterface
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("env %q, input %v: got %v, want %v", tc.env, tc.input, got, tc.want)
		}
	}
}

func TestDefaultLanInterfaceIsMarshalled(t *testing.T) {
	t.Setenv(DefaultLanInterfaceEnv, "br-lan")
	glob := &global.Input{LanInterface: &[]string{}}
	applyDefaultLanInterface(glob)
	s, err := glob.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s, "lan_interface") || !strings.Contains(s, "br-lan") {
		t.Fatalf("marshalled global lacks the default LAN interface:\n%s", s)
	}
}
