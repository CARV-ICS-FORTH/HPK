setup() {
    load "helper/bats-support/load"
    load "helper/bats-assert/load"
    load "helper/bats-detik/lib/utils"
    load "helper/bats-detik/lib/detik"

    export DETIK_CLIENT_NAME="kubectl"
    export TEST_DIR="$( cd "$( dirname "$BATS_TEST_FILENAME" )" >/dev/null 2>&1 && pwd )"
}

@test "diagnostics -> networking -> privileged-ports" {
    run kubectl create ns iperf
    assert_success

    DETIK_CLIENT_NAMESPACE="iperf"

    run kubectl apply -f $TEST_DIR/diagnostics/networking/privileged-ports.yaml
    assert_success
    
    sleep 3

    run verify "there is 1 pod named 'privileged-server'"
    assert_success
    run verify "there is 1 pod named 'privileged-client'"
    assert_success
    run verify "there is 1 service named 'privileged-server-service'"
    assert_success

    sleep 30

    run try "at most 20 times every 10s to find 1 pod named 'privileged-client' with 'status' being 'succeeded'"
    assert_success
    run verify "'status' is 'running' for pod named 'privileged-server'"
    assert_success

    run kubectl logs -n iperf privileged-server
    assert_output --partial "Server listening"
    run kubectl logs -n iperf privileged-client
    assert_output --partial "Connecting to host"
    assert_output --partial "iperf Done"

    run kubectl delete -f $TEST_DIR/diagnostics/networking/privileged-ports.yaml
    assert_success
    run kubectl delete ns iperf
    assert_success
}

@test "diagnostics -> networking -> unprivileged-ports" {
    run kubectl create ns iperf
    assert_success

    DETIK_CLIENT_NAMESPACE="iperf"

    run kubectl apply -f $TEST_DIR/diagnostics/networking/unprivileged-ports.yaml
    assert_success

    sleep 3

    run verify "there is 1 pod named 'unprivileged-server'"
    assert_success
    run verify "there is 1 pod named 'unprivileged-client'"
    assert_success
    run verify "there is 1 service named 'unprivileged-server-service'"
    assert_success

    sleep 30

    run try "at most 20 times every 10s to find 1 pod named 'unprivileged-client' with 'status' being 'succeeded'"
    assert_success
    run verify "'status' is 'running' for pod named 'unprivileged-server'"
    assert_success

    run kubectl logs -n iperf unprivileged-server
    assert_output --partial "Server listening"
    run kubectl logs -n iperf unprivileged-client
    assert_output --partial "Connecting to host"
    assert_output --partial "iperf Done"

    run kubectl delete -f $TEST_DIR/diagnostics/networking/unprivileged-ports.yaml
    assert_success
    run kubectl delete ns iperf
    assert_success
}
