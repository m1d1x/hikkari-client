CI puts the release bundle here as `hikkari.zip` (the `hikkari\` folder:
HikkariService.exe, hikkari-updater.exe, hikkari.exe, app\) before building
hikkari-setup.exe, which embeds this directory. A build without it is a
setup that says so instead of installing.
