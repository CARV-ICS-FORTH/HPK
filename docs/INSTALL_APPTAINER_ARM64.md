This is a guide for installing apptainer without root, on arm64 machines.

# Get the source codes

Firstly, create a build directory and download the source codes there.

```
mkdir builds
cd builds
```

If you can download them directly from the Internet, use:

```
wget https://go.dev/dl/go1.19.3.linux-arm64.tar.gz
wget https://github.com/apptainer/apptainer/releases/download/v1.1.3/apptainer-1.1.3.tar.gz
```

# Install GO

To install Go, we use the instructions on: https://go.dev/doc/install

```
tar -xzvf go1.19.3.linux-arm64.tar.gz
```

In practise, we see that golang is already compile for the target platform.
All we have to do now is to fix the `GOHOME`, `GOPATH`, and `PATH`

In the ~/.bashrc, append the following:

```
GOPATH=${HOME}/builds/go/bin
PATH=$PATH:${GOPATH}
```

To load the configuration, do `source ~/.bashrc`

Now, simply try `go` to verify the installation

# Install Apptainer

For apptainer, the process is similar.

```
tar -xzvf apptainer-1.1.3.tar.gz
cd apptainer
./mconfig --prefix=~/apptainer --without-suid --without-seccomp
make -e -C builddir/
make -C ./builddir install
```

by now, apptainer should be installed in $HOME/apptainer.

Like before, in the ~/.bashrc, append the following:

```
PATH=$PATH:${HOME}/apptainer/bin
```

and do  `source ~/.bashrc`

# Run a container

Download an arm64 container.

```
apptainer pull --arch arm64 arm64-ubuntu.sif docker://arm64v8/ubuntu
```

Run the container on the headnode

```
./apptainer shell ~/arm64-ubuntu.sif
```

# Use container in Slurm jobs

To submit slurms jobs, we need to wrap the command into a `script.sh`.

```
#!/bin/bash
#MSUB -r box_128_32r               # Request name
#MSUB -N 2
#MSUB -n 24                        # Total number of tasks to use
#MSUB -c 2                         # Number of threads per task to use
#MSUB -T 86300                     # Elapsed time limit in seconds
#MSUB -o output_%I                 # Standard output. %I is the job id
#MSUB -e error_%I                  # Error output. %I is the job id
#MSUB -q a64fx                     # Choosing nodes
#MSUB -A epxt310


ls
singularity shell ~/arm64-ubuntu.sif
```

Then, submit the script using `ccc_msub script.sh`

# Additional Sources:

https://git.alpinelinux.org/aports/tree/testing/apptainer/APKBUILD
https://sylabs.io/2022/07/cross-architecture-containers-with-singularityce-pro/
https://medium.com/@panda1100/apptainer-without-root-privilege-b21695ecfc42
https://jensd.be/1126/linux/cross-compiling-for-arm-or-aarch64-on-debian-or-ubuntu