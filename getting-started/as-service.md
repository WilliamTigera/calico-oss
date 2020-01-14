---
title: Running tigera/cnx-node with an init system
canonical_url: 'https://docs.tigera.io/v2.5/getting-started/as-service'
---

This guide explains how to run `{{site.nodecontainer}}` with an init system like
systemd, inside either of the following container types:
- [Docker](#running-{{site.nodecontainer}}-in-a-docker-container)
- [rkt](#running-{{site.nodecontainer}}-in-a-rkt-container)

## Running {{site.nodecontainer}} in a Docker container
{% include content/docker-container-service.md %}

## Running {{site.nodecontainer}} in a rkt container
{% include content/rkt-container-service.md %}
