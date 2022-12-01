
= Privileged Ports
2022/12/01 16:32:44 [emerg] 1#1: bind() to 0.0.0.0:80 failed (13: Permission denied)

Opening privileged port (like 80) require .. priviledges.

In Docker, this is feasible to port remapping on the host. However, we do not support such functionality.
Instead, containers must be designed to avoid priviledged ports.


