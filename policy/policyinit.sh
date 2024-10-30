#!/bin/bash
set -o errexit

mount -o remount rw /proc/sys

terway-cli policy