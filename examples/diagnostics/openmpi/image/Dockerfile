FROM mpioperator/openmpi-builder:v0.4.0 as builder

COPY pi.cc /src/pi.cc
RUN mpic++ /src/pi.cc -o /pi

FROM mpioperator/openmpi:v0.4.0


# @link https://unix.stackexchange.com/a/193131/311426
# On Linux, you can disable password-based access to an account while allowing
# SSH access (with some other authentication method, typically a key pair) with:
RUN usermod -p '*' mpiuser

COPY --from=builder /pi /home/mpiuser/pi
