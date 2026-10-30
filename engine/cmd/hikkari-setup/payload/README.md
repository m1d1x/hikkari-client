CI puts the release bundle here as `hikkari.zip` (the `Hikkari\` folder:
HikkariService.exe, hikkari-updater.exe, Hikkari.exe, app\) before building
HikkariSetup.exe, which embeds this directory. A build without it is a
setup that says so instead of installing.
