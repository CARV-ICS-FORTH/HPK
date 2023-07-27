# Frisbee Changelog

## Changes Since Last Release

### Changed defaults / behaviours
- Add flag to enable/disable support for cgroup v2.
- Add NoSupported msg on log following
- Moved snippets to /examples, and verify their behavior from scripts in /test.
- Move image Dockerfile folder from /deploy to /images
- ...

### New Features & Functionality
- Added several examples
- New corrupted pods to a central location for inspection
- ...

## Bug Fixes
- Fix issues with image naming when digest is part of the image's name.
- Fixed issues with non-existing HostPath
- Fix exiting of sbatch script when there is an issue with the constructor script.
- Fix issue with quotas inside the sbatch script.

## 0.1.0 \[2023-05-13\]