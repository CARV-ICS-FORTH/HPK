#!/bin/bash

sleep 5 && echo a && date &
sleep 10 && echo b && date &
sleep 5 && echo c && date  &
sleep 8 && echo d && date &


wait

sleep 5 && echo e && date  &
sleep 5 && echo f && date  &
sleep 5 && echo g && date  &


wait
