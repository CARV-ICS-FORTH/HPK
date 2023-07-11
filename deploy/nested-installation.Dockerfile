FROM fedora:39

RUN dnf -y install etcd singularity-ce



#RUN 	mkdir -p ${K8SFS_PATH}/log & \
#	singularity run --net --dns 8.8.8.8 --fakeroot \
#	--cleanenv --pid --containall \
#	--no-init --no-umask --no-eval \
#	--no-mount tmp,home --unsquash --writable \
#	--env K8SFS_MOCK_KUBELET=0 \
#	--bind ${K8SFS_PATH}:/usr/local/etc \
#	--bind ${K8SFS_PATH}/log:/var/log \
#	docker://chazapis/kubernetes-from-scratch:20221217
