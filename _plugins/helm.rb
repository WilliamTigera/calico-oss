require "jekyll"
require "tempfile"
require "open3"

require_relative "./lib"

# This plugin enables jekyll to render helm charts.
# Traditionally, Jekyll will render files which make use of the Liquid templating language.
# This plugin adds a new 'tag' that when specified will pass the input to the Helm binary.
# example use:
#
# {% helm %}
# datastore: kubernetes
# networking: calico
# {% endhelm %}
module Jekyll
  class RenderHelmTagBlock < Liquid::Block
    def initialize(tag_name, extra_args, liquid_options)
      super

      if extra_args.start_with?("tigera-secure-lma")
        @chart = "tigera-secure-lma"
        extra_args.slice! "tigera-secure-lma"
      else
        @chart = "calico"
      end

      @extra_args = extra_args
    end
    def render(context)
      text = super

      # Because helm hasn't merged stdin support, write the passed-in values.yaml
      # to a tempfile on disk.
      t = Tempfile.new("jhelm")
      t.write(text)
      t.close

      version = context.registers[:page]["version"]
      imageRegistry = context.registers[:page]["registry"]
      imageNames = context.registers[:site].config["imageNames"]
      versions = context.registers[:site].data["versions"]

      vs = parse_versions(versions, version)
      versionsYml = gen_values(vs, imageNames, imageRegistry)

      tv = Tempfile.new("temp_versions.yml")
      tv.write(versionsYml)
      tv.close

      # Execute helm.
      # Set the default etcd endpoint placeholder for rendering in the docs.
      cmd = """helm template _includes/#{version}/charts/#{@chart} \
        -f _includes/#{version}/charts/#{@chart}/base_values.yaml \
        -f #{tv.path} \
        -f #{t.path} \
        --set imagePullSecrets.cnx-pull-secret='' \
        --set manager.service.type=NodePort \
        --set manager.service.nodePort=30003 \
        --set alertmanager.service.type=NodePort \
        --set alertmanager.service.nodePort=30903 \
        --set prometheus.scrapeTargets.node.service.type=NodePort \
        --set prometheus.scrapeTargets.node.service.nodePort=30909 \
        --set kibana.service.type=NodePort \
        --set kibana.service.nodePort=30601 \
        --set etcd.endpoints='http://<ETCD_IP>:<ETCD_PORT>'"""

      cmd += " " + @extra_args.to_s

      out, stderr, status = Open3.capture3(cmd)
      if status != 0
        raise "failed to execute helm for '#{context.registers[:page]["path"]}': #{stderr}"
      end

      t.unlink
      tv.unlink
      return out
    end
  end
end

Liquid::Template.register_tag('helm', Jekyll::RenderHelmTagBlock)
