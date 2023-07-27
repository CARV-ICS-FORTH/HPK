package image_test

import (
	"testing"

	"github.com/carv-ics-forth/hpk/compute"
	"github.com/carv-ics-forth/hpk/compute/runtime"
)

func Test_ExecAsFakeroot(t *testing.T) {
	tests := []struct {
		name    string
		cmd     []string
		wantErr bool
	}{
		{
			name:    "create directory",
			cmd:     []string{"mkdir", compute.HPK.String() + "/pirate"},
			wantErr: false,
		},
		{
			name:    "touch files",
			cmd:     []string{"touch", compute.HPK.String() + "/pirate/hook"},
			wantErr: false,
		},
		{
			name:    "ls files",
			cmd:     []string{"ls", "-lah", compute.HPK.String()},
			wantErr: false,
		},
		{
			name:    "remove files",
			cmd:     []string{"rm", "-rf", compute.HPK.String() + "/pirate"},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := runtime.DefaultPauseImage.FakerootExec(tt.cmd...); (err != nil) != tt.wantErr {
				t.Errorf("ExecAsFakeroot() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
