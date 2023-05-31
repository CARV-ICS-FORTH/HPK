#!/bin/bash

HOST_NAME=$(hostname -s)
HOST_ADDRESS=$(ip route get 1 | sed -n 's/.*src \([0-9.]\+\).*/\1/p')

CPUS=$(lscpu | grep -E '^CPU\(' | cut -d':' -f2 | tr -d '[:space:]')
SOCKETS=$(lscpu | grep -E '^Socket' | cut -d':' -f2 | tr -d '[:space:]')
CORES_PER_SOCKET=$(lscpu | grep -E '^Core' | cut -d':' -f2 | tr -d '[:space:]')
THREADS_PER_CORE=$(lscpu | grep -E '^Thread' | cut -d':' -f2 | tr -d '[:space:]')

# Install Slurm
apt-get update
apt-get install -y slurmd slurm-client slurmctld

cat >/etc/slurm/slurm.conf << EOF
AuthType=auth/none
CredType=cred/none
MpiDefault=none
ProctrackType=proctrack/linuxproc
ReturnToService=1
SlurmctldPidFile=/var/run/slurmctld.pid
SlurmctldPort=6817
SlurmdPidFile=/var/run/slurmd.pid
SlurmdPort=6818
SlurmdSpoolDir=/var/spool/slurmd
SlurmUser=root
StateSaveLocation=/var/spool
SwitchType=switch/none
TaskPlugin=task/affinity
InactiveLimit=0
KillWait=30
MinJobAge=300
SlurmctldTimeout=120
SlurmdTimeout=300
Waittime=0
SchedulerType=sched/backfill
SelectType=select/cons_tres
SelectTypeParameters=CR_Core
AccountingStorageType=accounting_storage/none
ClusterName=cluster
JobCompType=jobcomp/none
JobAcctGatherFrequency=30
JobAcctGatherType=jobacct_gather/none
SlurmctldDebug=info
SlurmdDebug=info

SlurmctldHost=${HOST_NAME} ($HOST_ADDRESS)
NodeName=${HOST_NAME} CPUs=${CPUS} SocketsPerBoard=${SOCKETS} CoresPerSocket=${CORES_PER_SOCKET} ThreadsPerCore=${THREADS_PER_CORE} State=UNKNOWN
PartitionName=hpk Nodes=${HOST_NAME} Default=YES MaxTime=INFINITE State=UP
EOF
systemctl enable slurmd
systemctl start slurmd
systemctl enable slurmctld
systemctl start slurmctld
