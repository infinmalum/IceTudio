#!/usr/bin/env python3
"""Package an already-built IceTudio binary without downloading tools.
The output directory is created separately; application data is never copied.
"""
import argparse, pathlib, shutil, subprocess, tarfile, tempfile, zipfile, platform
p=argparse.ArgumentParser();p.add_argument('--os',choices=['linux','windows','darwin'],default={'Darwin':'darwin','Windows':'windows'}.get(platform.system(),'linux'));p.add_argument('--binary',required=True);p.add_argument('--plugin-helpers',action='store_true',help='Include the two explicitly built Linux plugin helpers');p.add_argument('--output',default='bin/packages');p.add_argument('--version',default='0.1.0');a=p.parse_args()
root=pathlib.Path(__file__).resolve().parent.parent
binary=pathlib.Path(a.binary).resolve()
if not binary.is_file():p.error('Build the target binary first: '+str(binary))
helpers = ('icetudio-plugin-scan', 'icetudio-plugin-run')
if a.plugin_helpers and (a.os != 'linux' or any(not binary.with_name(name).is_file() or binary.with_name(name).is_symlink() for name in helpers)):
 p.error('Build both Linux plugin helpers beside the application before packaging them')

out=pathlib.Path(a.output).resolve();out.mkdir(parents=True,exist_ok=True)
with tempfile.TemporaryDirectory(prefix='icetudio-package-') as temp:
 folder=pathlib.Path(temp)/'IceTudio';folder.mkdir()
 if a.os=='darwin':
  bundle=folder/'IceTudio.app'/'Contents';target=bundle/'MacOS';target.mkdir(parents=True);resources=bundle/'Resources';resources.mkdir();shutil.copy2(root/'build/darwin/Info.plist',bundle/'Info.plist')
 else:target=folder;resources=folder
 name='icetudio.exe' if a.os=='windows' else 'icetudio';shutil.copy2(binary,target/name);(target/name).chmod(0o755)
 if a.plugin_helpers:
  for name in helpers:
   shutil.copy2(binary.with_name(name), target/name)
   (target/name).chmod(0o755)
 if a.os=='darwin' and platform.system()=='Darwin':
  iconset=pathlib.Path(temp)/'icons.iconset';iconset.mkdir()
  for size in [16,32,128,256,512]:
   for multiplier in [1,2]:
    filename='icon_'+str(size)+'x'+str(size)+('@2x' if multiplier==2 else '')+'.png'
    subprocess.run(['sips','-z',str(size*multiplier),str(size*multiplier),str(root/'build/appicon.png'),'--out',str(iconset/filename)],check=True,stdout=subprocess.DEVNULL)
  subprocess.run(['iconutil','-c','icns',str(iconset),'-o',str(resources/'icons.icns')],check=True)
 shutil.copy2(root/'LICENSE',resources/'LICENSE');shutil.copytree(root/'licenses',resources/'licenses')
 vendor=root/'frontend/public/vendor/gridsound';shutil.copytree(vendor,resources/'third-party/gridsound',ignore=shutil.ignore_patterns('*.js','*.css','*.html','*.json'))
 sf=root/'assets/soundfonts';notice=resources/'soundfont-notices';notice.mkdir()
 for f in sf.iterdir():
  if f.is_file() and f.suffix.lower() in ['.md','.csv','.txt']:shutil.copy2(f,notice/f.name)
 font=sf/'MuseScore_General.sf3'
 if font.exists():
  import hashlib
  if hashlib.sha256(font.read_bytes()).hexdigest()!='5b85b6c2c61d10b2b91cddd41efcce7b25cd31c8271d511c73afafbef20b6fa3':raise SystemExit('Soundfont checksum mismatch; refusing to package it')
  shutil.copy2(font,target/font.name)
 (resources/'RUNNING.txt').write_text('IceTudio '+a.version+'\nNo model is required for MIDI editing.\nLinux: GTK 3, WebKitGTK 4.1 and FluidSynth >= 2.6 are system prerequisites.\nWindows: WebView2 runtime is required. macOS: system WebKit.\nUser-selected soundfonts and application data are not included.\nA missing licensed GM soundfont uses the existing Basic Waves fallback.\n',encoding='utf-8')
 if a.plugin_helpers:
  with (resources/'RUNNING.txt').open('a', encoding='utf-8') as f:
   f.write('CLAP/VST3: Bubblewrap and prlimit, with Linux user namespaces enabled. Optional vendor editors need Xephyr and an X11/Xwayland desktop. No third-party plugins are included.\n')
 if a.os=='darwin':shutil.copytree(folder/'IceTudio.app',out/'IceTudio.app',dirs_exist_ok=True)
 stem='icetudio-'+a.version+'-'+a.os+'-'+platform.machine()
 if a.os=='linux':
  dest=out/(stem+'.tar.gz')
  with tarfile.open(dest,'w:gz') as archive:archive.add(folder,arcname='IceTudio')
 else:
  dest=out/(stem+'.zip')
  with zipfile.ZipFile(dest,'w',zipfile.ZIP_DEFLATED) as archive:
   for f in folder.rglob('*'):
    if f.is_file():archive.write(f,f.relative_to(folder.parent))
 print(dest)
