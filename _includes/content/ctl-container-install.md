## Installing {{include.cli}} as a container on a single host

1. Ensure that you have the [`config.json` file with the private Tigera registry credentials]({{site.baseurl}}/getting-started/calico-enterprise#get-private-registry-credentials-and-license-key).

1. From a terminal prompt, use the following command to either create or open the `~/.docker/config.json` file.

   ```bash
   vi ~/.docker/config.json
   ```

1. Depending on the existing contents of the file, edit it in one of the following ways.

   - **New file**: Paste in the entire contents of the `config.json` file from Tigera.

   - **Existing file without quay.io object**: Add the following lines from the `config.json` inside the `"auth"` object.

     ```json
     "quay.io": {
       "auth": "<ROBOT-TOKEN-VALUE>",
       "email": ""
     }
     ```

   - **Existing file with quay.io object**: Add the following lines from the `config.json` inside the `"quay.io"` object.

     ```json
     "auth": "<ROBOT-TOKEN-VALUE>",
     "email": ""
     ```

1. Save and close the file.

1. Use the following commands to pull the `{{include.cli}}` image from the Tigera
   registry.

   ```bash
   docker pull {{page.registry}}{% include component_image component=include.cli %}
   ```

1. Confirm that the image has loaded by typing `docker images`.
{%- assign c = site.data.versions.first.components[include.cli] %}
   ```bash
   REPOSITORY                TAG               IMAGE ID       CREATED         SIZE
   {{ c.image }}    {{ c.version }}            e07d59b0eb8a   2 minutes ago   42MB
   ```
   {: .no-select-button}

**Next step**:
[Configure `{{include.cli}}` to connect to your datastore]({{site.baseurl}}/getting-started/clis/{{include.cli}}/configure/).
