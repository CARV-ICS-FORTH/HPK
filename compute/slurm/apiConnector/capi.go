package apiConnector

/*
#cgo LDFLAGS: -lslurm
#include <slurm/slurm.h>
#include <stdlib.h>
*/
import "C"
import (
	"fmt"
	"unsafe"
)

// import "fmt"

func Connect() bool {
	// Initialize the SLURM API
	C.slurm_init(nil)

	var clusterInfo *C.slurm_conf_t

	if C.SLURM_SUCCESS != C.slurm_load_ctl_conf(0, &clusterInfo) {
		msg := C.CString("\nCould not connect to the slurm cluster\n")
		C.slurm_perror(msg)
		C.free(unsafe.Pointer(msg))
		C.slurm_free_ctl_conf(clusterInfo)
		C.slurm_fini()
		return false
	}

	fmt.Println("Connection OK with slurm")
	defer C.slurm_free_ctl_conf(clusterInfo)

	defer C.slurm_fini()
	return true

	// // Extract and print cluster information
	// // You can continue to use other SLURM API functions to perform various operations

	// // For example, querying job information
	// var jobIter C.struct_slurm_job_iter
	// C.slurm_init_job_iter(&jobIter, 0, C.SLURM_JOB_COMPLETE, 0)

	// for {
	// 	jobInfo := C.slurm_get_job_next(&jobIter)
	// 	if jobInfo == nil {
	// 		break
	// 	}
	// 	defer C.slurm_free_job_info_msg(jobInfo)

	// 	// Process job information
	// 	fmt.Printf("Job ID: %d, State: %s\n", jobInfo.job_id, C.GoString(jobInfo.job_state))
	// }
}
