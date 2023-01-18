# Spark using the Spark Operator

```bash
helm repo add spark-operator https://googlecloudplatform.github.io/spark-on-k8s-operator
helm install spark-operator spark-operator/spark-operator \
--set enableWebhook=true

cat <<EOF | kubectl apply -f -
---
apiVersion: "sparkoperator.k8s.io/v1beta2"
kind: SparkApplication
metadata:
    name: spark-pi
    namespace: default
spec:
    type: Scala
    mode: cluster
    image: "carvicsforth/spark:3.3.1"
    mainClass: org.apache.spark.examples.SparkPi
    mainApplicationFile: "local:///opt/spark/examples/jars/spark-examples_2.12-3.3.1.jar"
    sparkVersion: "3.3.1"
    restartPolicy:
        type: Never
    driver:
        serviceAccount: default
        labels:
            version: 3.3.1
    executor:
        instances: 2
        labels:
            version: 3.3.1
EOF
```

Follow the status using:

```bash
kubectl describe sparkapplication spark-pi
```

The result is show in the driver logs:

```bash
kubectl logs -f spark-pi-driver
```