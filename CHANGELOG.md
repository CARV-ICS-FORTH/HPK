# Frisbee Changelog

## Changes Since Last Release

### Changed defaults / behaviours
- Add flag to enable/disable support for cgroup v2.
- Add NoSupported msg on log following
- Moved snippets to /examples, and verify their behavior from scripts in /test.
- Move image Dockerfile folder from /deploy to /images
- ...

### New Features & Functionality
- Added several examples with respective tests.
- New corrupted pods to a central location for inspection
- Add examples for OpenGadget
- Add support for IPC
- Add snippets for cloud-native mpi executions with cgroup
- Set temporary workdir for pause containers
- ...

## Bug Fixes
- Fix issues with image naming when digest is part of the image's name.
- Fixed issues with non-existing HostPath
- Fix exiting of sbatch script when there is an issue with the constructor script.
- Fix issue with quotas inside the sbatch script.
- Work on how objects are being deleted (Slurm jobs, strange permissions on volumes, ...)
- In a nested select within a loop in the Slurm listener we used "continue" whereas "break" had to be used.

## 0.1.0 \[2023-05-13\]
