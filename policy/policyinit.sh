#!/bin/bash
set -o errexit

mount -o remount rw /proc/sys

exec terway-cli policy