#! /usr/bin/env bash

nix-shell -p rsync --run 'rsync -ah ~/code/powerhouse powerhouse:code/ --info=progress2'
ssh -t powerhouse sudo nixos-rebuild switch
