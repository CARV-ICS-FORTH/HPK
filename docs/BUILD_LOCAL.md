```
# Start a docker registry
$ docker run -d -p 5000:5000 --restart=always --name registry registry:2
# Push local docker container to it
$ docker tag alpine localhost:5000/alpine
$ docker push localhost:5000/alpine
```

```
SINGULARITY_NOHTTPS=1 singularity shell docker://localhost:5000/alpine
```

# Sources

https://groups.google.com/a/lbl.gov/g/singularity/c/ZXOaNaM_6MY
