setup() {
    load "helper/bats-support/load"
    load "helper/bats-assert/load"
    load "helper/bats-detik/lib/utils"
    load "helper/bats-detik/lib/detik"

    export DETIK_CLIENT_NAME="kubectl"
    export TEST_DIR="$( cd "$( dirname "$BATS_TEST_FILENAME" )" >/dev/null 2>&1 && pwd )"
}

@test "diagnostics -> ipc " {
    DETIK_CLIENT_NAMESPACE="ipc"
    cd "./examples/diagnostics/${DETIK_CLIENT_NAMESPACE}"

    # clean-up any previous environment
    run ./uninstall.sh || echo "No previous installation was found"

    # deploy the app
    run ./install.sh
    assert_success

    # Ensure that containers complete
    try "at most 5 times every 30s to get pods named 'shared-memory' and verify that 'status' is 'succeeded'"

    # undeploy the app
    run ./uninstall.sh
    assert_success
}


@test "diagnostics -> openmpi " {
    DETIK_CLIENT_NAMESPACE="openmpi"
    cd "./examples/diagnostics/${DETIK_CLIENT_NAMESPACE}"

    # clean-up any previous environment
    run ./uninstall.sh || echo "No previous installation was found"

    # deploy the app
    run ./install.sh
    assert_success

    # Ensure that containers complete
    try "at most 5 times every 30s to get pods named 'launcher' and verify that 'status' is 'succeeded'"
    try "at most 5 times every 30s to get pods named 'workers-1' and verify that 'status' is 'running'"

    # undeploy the app
    run ./uninstall.sh
    assert_success
}


@test "diagnostics -> scripting -> quotes" {
    DETIK_CLIENT_NAMESPACE="scripting-quotes"
    cd "./examples/diagnostics/${DETIK_CLIENT_NAMESPACE}"

    # clean-up any previous environment
    run ./uninstall.sh || echo "No previous installation was found"

    # deploy the app
    run ./install.sh
    assert_success

    # Ensure that containers complete
    try "at most 5 times every 30s to get pods named 'double-quotes' and verify that 'status' is 'succeeded'"
    try "at most 5 times every 30s to get pods named 'mixed-quotes' and verify that 'status' is 'succeeded'"

    # undeploy the app
    run ./uninstall.sh
    assert_success
}

@test "diagnostics -> volumes -> hostpath" {
    DETIK_CLIENT_NAMESPACE="volume-hostpath"
    cd "./examples/diagnostics/${DETIK_CLIENT_NAMESPACE}"

    # clean-up any previous environment
    run ./uninstall.sh || echo "No previous installation was found"

    # deploy the app
    run ./install.sh
    assert_success

    # Ensure that containers complete
    try "at most 5 times every 30s to get pods named 'entire-volume' and verify that 'status' is 'succeeded'"
    try "at most 5 times every 30s to get pods named 'subpath' and verify that 'status' is 'succeeded'"
    try "at most 5 times every 30s to get pods named 'wrongpath' and verify that 'status' is 'failed'"

    # Ensure that containers behave correctly
    run kubectl logs -n ${DETIK_CLIENT_NAMESPACE} entire-volume
    assert_output --partial "pirate"
    assert_output --partial "privateer"

    run kubectl logs -n ${DETIK_CLIENT_NAMESPACE} subpath
    assert_output --partial "pirate"

    # undeploy the app
    run ./uninstall.sh
    assert_success
}

@test "diagnostics -> volumes -> init" {
    DETIK_CLIENT_NAMESPACE="volume-init"
    cd "./examples/diagnostics/${DETIK_CLIENT_NAMESPACE}"

    # clean-up any previous environment
    run ./uninstall.sh || echo "No previous installation was found"

    # deploy the app
    run ./install.sh
    assert_success

    # Ensure that containers complete
    try "at most 5 times every 30s to get pods named 'volume-initializer' and verify that 'status' is 'succeeded'"

    # undeploy the app
    run ./uninstall.sh
    assert_success
}


@test "diagnostics -> webhooks -> cert-manager" {
    DETIK_CLIENT_NAMESPACE="webhooks"
    cd "./examples/diagnostics/${DETIK_CLIENT_NAMESPACE}"

    # clean-up any previous environment
    run ./uninstall.sh || echo "No previous installation was found"

    # deploy the app
    run ./install.sh
    assert_success

    # Ensure that containers complete
    try "at most 5 times every 30s to get pods named 'cert-manager' and verify that 'status' is 'running'"
    try "at most 5 times every 30s to get pods named 'cert-manager-cainjector' and verify that 'status' is 'running'"
    try "at most 5 times every 30s to get pods named 'cert-manager-webhook' and verify that 'status' is 'running'"

    # The following causes fluctuations and is excluded.
    # try "at most 5 times every 30s to get pods named 'cert-manager-startupapicheck' and verify that 'status' is 'running'"

    # undeploy the app
    run ./uninstall.sh
    assert_success
}


@test "diagnostics -> users -> custom-users" {
    DETIK_CLIENT_NAMESPACE="custom-users"
    cd "./examples/diagnostics/${DETIK_CLIENT_NAMESPACE}"

    # clean-up any previous environment
    run ./uninstall.sh || echo "No previous installation was found"

    # deploy the app
    run ./install.sh
    assert_success

    # Ensure that containers complete
    try "at most 5 times every 30s to get pods named 'whoami-adduser' and verify that 'status' is 'succeeded'"
    try "at most 5 times every 30s to get pods named 'whoami-default' and verify that 'status' is 'succeeded'"
    try "at most 5 times every 30s to get pods named 'whoami-newuser' and verify that 'status' is 'succeeded'"
    try "at most 5 times every 30s to get pods named 'whoami-nobody' and verify that 'status' is 'succeeded'"
    try "at most 5 times every 30s to get pods named 'whoami-root' and verify that 'status' is 'succeeded'"

    # Ensure that containers behave correctly
    run kubectl logs -n ${DETIK_CLIENT_NAMESPACE} whoami-default
    assert_output --partial "newuser"

    run kubectl logs -n ${DETIK_CLIENT_NAMESPACE} whoami-newuser
    assert_output --partial "newuser"

    run kubectl logs -n ${DETIK_CLIENT_NAMESPACE} whoami-nobody
    assert_output --partial "nobody"

    run kubectl logs -n ${DETIK_CLIENT_NAMESPACE} whoami-root
    assert_output --partial "root"

    # undeploy the app
    run ./uninstall.sh
    assert_success
}

@test "diagnostics -> networking -> localhost" {
    DETIK_CLIENT_NAMESPACE="networking-localhost"
    cd "./examples/diagnostics/${DETIK_CLIENT_NAMESPACE}"

    # clean-up any previous environment
    run ./uninstall.sh || echo "No previous installation was found"

    # deploy the app
    run ./install.sh
    assert_success

    echo "Ensure that the deployment becomes running"
    verify "there is 1 pod named 'iperf.localhost'"

    # Ensure that client completes
    try "at most 10 times every 30s to get pods named 'iperf.localhost' and verify that 'status' is 'Running'"

    # undeploy the app
    run ./uninstall.sh
    assert_success
}

@test "diagnostics -> networking -> unprivileged-ports" {
    DETIK_CLIENT_NAMESPACE="networking-unprivileged"
    cd "./examples/diagnostics/${DETIK_CLIENT_NAMESPACE}"

    # clean-up any previous environment
    run ./uninstall.sh || echo "No previous installation was found"

    # deploy the app
    run ./install.sh
    assert_success

    echo "Ensure that the deployment becomes running"
    verify "there is 1 service named 'server-service'"
    verify "there is 1 pod named 'server'"
    verify "there is 1 pod named 'client'"

    # Ensure that client completes
    try "at most 5 times every 30s to get pods named 'client' and verify that 'status' is 'succeeded'"
    try "at most 5 times every 5s to get pods named 'server' and verify that 'status' is 'running'"

    # undeploy the app
    run ./uninstall.sh
    assert_success
}

@test "diagnostics -> networking -> privileged-ports" {
    DETIK_CLIENT_NAMESPACE="networking-privileged"
    cd "./examples/diagnostics/${DETIK_CLIENT_NAMESPACE}"

    # clean-up any previous environment
    run ./uninstall.sh || echo "No previous installation was found"

    # deploy the app
    run ./install.sh
    assert_success

    echo "Ensure that the deployment becomes running"
    verify "there is 1 service named 'server-service'"
    verify "there is 1 pod named 'server'"
    verify "there is 1 pod named 'client'"

    # Ensure that client completes
    try "at most 5 times every 30s to get pods named 'client' and verify that 'status' is 'succeeded'"
    try "at most 5 times every 5s to get pods named 'server' and verify that 'status' is 'running'"

    # undeploy the app
    run ./uninstall.sh
    assert_success
}

