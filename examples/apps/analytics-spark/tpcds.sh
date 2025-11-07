#!/bin/bash

# New lines to add to .bashrc
NEW_LINES='
export KUBE_PATH=~/.hpk-master/kubernetes/
export KUBECONFIG=${KUBE_PATH}/admin.conf'

# Check if lines already exist in .bashrc
if ! grep -qF "$NEW_LINES" ~/.bashrc; then
    # Append the lines to .bashrc
    echo -e "\n$NEW_LINES" >> ~/.bashrc
    echo "Environment variables added to ~/.bashrc"
else
    echo "Environment variables already exist in ~/.bashrc"
fi

# run TPCDS benchmark
#cd HPK/examples/apps/analytics-spark

# Pod name and namespace
DATA_GENERATION_DRIVER_POD_NAME="tpcds-benchmark-data-generation-1g-driver"
BENCHMARK_DRIVER_POD_NAME="tpcds-benchmark-sql-1g-driver"
NAMESPACE="default"


# check spark-operator controller status
check_controller_status() {
    kubectl get pod -l app.kubernetes.io/component=controller,app.kubernetes.io/instance=spark-operator -n analytics-spark -o jsonpath='{.items[0].status.phase}'
}
# check data generation pod status
check_data_generation_status() {
    kubectl get pod "$DATA_GENERATION_DRIVER_POD_NAME" -n "$NAMESPACE" -o jsonpath='{.status.phase}'
}
# check benchmark pod status
check_benchmark_status() {
    kubectl get pod "$BENCHMARK_DRIVER_POD_NAME" -n "$NAMESPACE" -o jsonpath='{.status.phase}'
}

# install spark-operator and minio for storage
pushd ~/HPK/examples/apps/analytics-spark
./install.sh

#wait for the controller to reach desired state
while true; do
    POD_STATUS=$(check_controller_status)
    if [ "$POD_STATUS" == "Running" ]; then
        echo "Spark Operator Controller running in namespace analytics-spark."
        break
    else
        echo "Spark Operator Controller in status $POD_STATUS."
        sleep 30
    fi
done

sleep 10
kubectl apply -f manifest-tpcds-data-generation.yaml -n "$NAMESPACE"
# Wait for the pod to reach the desired state
while true; do
    POD_STATUS=$(check_data_generation_status)
    if [ "$POD_STATUS" == "Succeeded" ]; then
        echo "Pod $DATA_GENERATION_DRIVER_POD_NAME in namespace $NAMESPACE has completed successfully."
        break
    elif [ "$POD_STATUS" == "Failed" ]; then
        echo "Pod $DATA_GENERATION_DRIVER_POD_NAME in namespace $NAMESPACE has failed."
        exit 1
    elif [ "$POD_STATUS" == "Unknown" ]; then
        echo "Pod $DATA_GENERATION_DRIVER_POD_NAME in namespace $NAMESPACE is in an unknown state."
        exit 1
    else
        echo "Waiting for the pod $DATA_GENERATION_DRIVER_POD_NAME in namespace $NAMESPACE to complete. Current status: $POD_STATUS"
        sleep 60
    fi
done

kubectl apply -f manifest-tpcds-benchmark.yaml -n "$NAMESPACE"
# Wait for the pod to reach the desired state
while true; do
    POD_STATUS=$(check_benchmark_status)
    if [ "$POD_STATUS" == "Succeeded" ]; then
        echo "Pod $BENCHMARK_DRIVER_POD_NAME in namespace $NAMESPACE has completed successfully."
        break
    elif [ "$POD_STATUS" == "Failed" ]; then
        echo "Pod $BENCHMARK_DRIVER_POD_NAME in namespace $NAMESPACE has failed."
        exit 1
    elif [ "$POD_STATUS" == "Unknown" ]; then
        echo "Pod $BENCHMARK_DRIVER_POD_NAME in namespace $NAMESPACE is in an unknown state."
        exit 1
    else
        echo "Waiting for the pod $BENCHMARK_DRIVER_POD_NAME in namespace $NAMESPACE to complete. Current status: $POD_STATUS"
        sleep 60
    fi
done
echo "TCPDS Benchmark executed succesfully."
popd
# ./uninstall.sh
