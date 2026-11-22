// Yarn Berry plugin that glab dependency-firewall injects through YARN_PLUGINS.
//
// Berry applies per-host networkSettings over the global httpProxy/httpsProxy
// that glab pins from the environment, and a map setting cannot be pinned from
// the environment. Every .yarnrc.yml Berry reads (the project tier up to the
// filesystem root, and the home file) can therefore route a registry host
// around the inspection proxy. Plugins named in YARN_PLUGINS load before any
// rc-file plugin, and wrapNetworkRequest runs before Berry resolves the
// settings for a request, so emptying the merged map here leaves every request
// on the pinned global proxy.
module.exports = {
  name: `@gitlab/plugin-dependency-firewall`,
  factory: require => {
    const {YarnVersion} = require(`@yarnpkg/core`);
    const {npath} = require(`@yarnpkg/fslib`);
    const [major, minor] = String(YarnVersion).split(`.`).map(Number);
    // wrapNetworkRequest exists from Yarn 3.2. An older or unknown version
    // would load the plugin without ever calling the hook.
    if (!(major > 3 || (major === 3 && minor >= 2)))
      throw new Error(`The GitLab Dependency Firewall requires Yarn 3.2 or later; this project uses Yarn ${YarnVersion}.`);

    return {
      hooks: {
        wrapNetworkRequest: async (executor, {configuration}) => {
          configuration.values.set(`networkSettings`, new Map());
          for (const key of [`httpsCaFilePath`, `caFilePath`])
            if (configuration.values.get(key))
              configuration.values.set(key, npath.toPortablePath(process.env.NODE_EXTRA_CA_CERTS));
          return executor;
        },
      },
    };
  },
};
