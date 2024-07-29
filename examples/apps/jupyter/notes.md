## Issues with jupyterhub that required modifying the manifest YAMLs for it to be deployed manually

for some reason enviroment variables inside the jupyter_config.py, that is defined inside the configmap yaml, are not evaluated into PORTS, i.e. HUB_SERVICE_PORT is never evaluated into 8081.

also DNS names are not being resolved, i.e. http://hub:8081 is never resolved and it is only resolved when it is stated with the full name like http://hub.jhub.svc.cluster.local:8081

also removed the need for pvc's bcs i didnt have dynamic storage provisioner and used none for testing purposes

also disabled the subPath of the static pvc because it doesnt mount properly (the subpath is created as a file and not as a symlink)