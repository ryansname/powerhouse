#! /usr/bin/env bash

git -C ~/code/powerhouse rev-parse --short HEAD > ~/code/powerhouse/voltage-repeater/VERSION
nix-shell -p rsync --run 'rsync -ah ~/code/powerhouse powerhouse:code/ --info=progress2 --filter=":- .gitignore" --exclude=.git'
ssh -t powerhouse sudo nixos-rebuild switch
