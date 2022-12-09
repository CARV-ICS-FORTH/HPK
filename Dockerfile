# Build the HPK operator binary
FROM golang:1.19 as builder

WORKDIR /build

# Copy the Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum

# cache deps before building and copying source so that we don't need to re-download as much
# and so that source changes don't invalidate our downloaded layer
RUN go mod download

# Copy the project's source code (except for whatever is included in the .dockerignore)
COPY . .

# Build release
#RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build  -a -o /hpk ./cmd/hpk-kubelet

# Build dev
RUN CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build -race -a -o /hpk ./cmd/hpk-kubelet


# Super minimal image just to package the hpk binary. It does not include anything.
# Seriously, nothing. Not even shell to login.
# We rely on Apptainer to mount all the peripheral mountpoints of the host HPC environment.
#
# For example:
# apptainer run --bind /usr/bin docker://icsforth/hpk:latest
FROM scratch

COPY --from=builder /hpk /hpk

ENTRYPOINT ["/hpk"]