# Running in AWS

## Setup the environment

Follow the [instructions](https://docs.aws.amazon.com/cli/latest/userguide/getting-started-install.html) to install the `aws` CLI.

Then everything else:
```sh
python3 -m venv venv
source venv/bin/activate
pip install --upgrade pip
pip install aws-parallelcluster Flask==2.2.5

# Install Node.js and npm (on macOS used "port install nodejs18 npm9")
npm install aws-cdk
export PATH=$PWD/node_modules/.bin:$PATH
```

Follow the [instructions](https://docs.aws.amazon.com/parallelcluster/latest/ug/install-v3-configuring.html) to configure and run a cluster:
```sh
aws configure
aws ec2 create-key-pair \
    --key-name hpk-cluster-keypair \
    --key-type rsa \
    --key-format pem \
    --query "KeyMaterial" \
    --output text > hpk-cluster-keypair.pem
chmod 400 hpk-cluster-keypair.pem

pcluster configure --config cluster-config.yaml
# Edit cluster-config.yaml, set MinCount equal to MaxCount to have all worker nodes immediately available, add OnNodeConfigured scripts (see the `cluster-config.yaml` example)
pcluster create-cluster --cluster-configuration cluster-config.yaml --cluster-name hpk-cluster --region eu-central-1
pcluster list-clusters
```

Wait until the last command shows `CREATE_COMPLETE`, then login:
```sh
pcluster ssh --cluster-name hpk-cluster -i ./hpk-cluster-keypair.pem
```

## Run HPK

Back to the head node, as the local user:
```sh
git clone https://github.com/CARV-ICS-FORTH/HPK.git
cd HPK

# Download the hpk-kubelet binary (adjust the version in the URL)
# wget https://github.com/CARV-ICS-FORTH/HPK/releases/download/v0.1.0/hpk-kubelet_v0.1.0_linux_amd64.tar.gz
wget https://github.com/CARV-ICS-FORTH/HPK/releases/download/v0.1.2/hpk-kubelet_v0.1.2_linux_amd64.tar.gz
tar -zxvf hpk-kubelet_v0.1.2_linux_amd64.tar.gz
mkdir -p bin
mv hpk-kubelet bin/
```

Run each of the following in a separate window:
```sh
make run-kubemaster
make run-kubelet
```

And you are all set:
```sh
export KUBE_PATH=~/.hpk-master/kubernetes/
export KUBECONFIG=${KUBE_PATH}/admin.conf
kubectl get nodes
```

## Clean up the environment

To clean up the cluster:
```sh
pcluster delete-cluster --region eu-central-1 --cluster-name hpk-cluster
```

To clean up the networks (VPCs), list them and then delete any ones that show up:
```sh
aws --region eu-central-1 cloudformation list-stacks --stack-status-filter "CREATE_COMPLETE" --query "StackSummaries[].StackName" | grep -e "parallelclusternetworking-"
aws --region eu-central-1 cloudformation delete-stack --stack-name <stack_name>
```
