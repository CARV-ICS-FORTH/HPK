package paths

import (
	"reflect"
	"testing"

	"k8s.io/apimachinery/pkg/types"
)

func TestHPKPath_ParseAbsPath(t *testing.T) {

	rootDir := "/home/fnikol/"

	p := HPK(rootDir)

	tests := []struct {
		name         string
		path         string
		wantPodKey   types.NamespacedName
		wantFileName string
		wantInvalid  bool
	}{
		{
			name:         "empty",
			path:         "",
			wantPodKey:   types.NamespacedName{},
			wantFileName: "",
			wantInvalid:  true,
		},

		{
			name: "abspath",
			path: "/home/fnikol/.hpk/default/volume-user/reviews.exitCode",
			wantPodKey: types.NamespacedName{
				Namespace: "default",
				Name:      "volume-user",
			},
			wantFileName: "reviews.exitCode",
			wantInvalid:  false,
		},
		{
			name:         "relpath",
			path:         "./hpk" + "/default/volume-user/reviews.exitCode",
			wantPodKey:   types.NamespacedName{},
			wantFileName: "",
			wantInvalid:  true, // only absolute path is support
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotPodKey, gotFileName, gotInvalid := p.ParseAbsPath(tt.path)
			if !reflect.DeepEqual(gotPodKey, tt.wantPodKey) {
				t.Errorf("ParseAbsPath() gotPodKey = %v, want %v", gotPodKey, tt.wantPodKey)
			}
			if gotFileName != tt.wantFileName {
				t.Errorf("ParseAbsPath() gotFileName = %v, want %v", gotFileName, tt.wantFileName)
			}
			if gotInvalid != tt.wantInvalid {
				t.Errorf("ParseAbsPath() gotInvalid = %v, want %v", gotInvalid, tt.wantInvalid)
			}
		})
	}
}
