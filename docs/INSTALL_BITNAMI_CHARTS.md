


helm install mongodb-dev bitnami/mongodb --set securityContext.fsGroup=0,securityContext.runAsUser=0

helm install mongodb-dev bitnami/mongodb --set securityContext.fsGroup=0,securityContext.runAsUser=0 --set persistence.enabled=false

# Sources

https://github.com/bitnami/bitnami-docker-rabbitmq/issues/109



https://podman.io/blogs/2018/10/03/podman-remove-content-homedir.html