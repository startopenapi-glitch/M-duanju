#!/usr/bin/env python3
"""Isolated, fail-closed gate. Never sources production env; never deploys."""
import os,sys,json,re,subprocess,shutil
from pathlib import Path
from urllib.parse import urlparse
root=Path(__file__).resolve().parents[1];out=root/'.test'/'final';out.mkdir(parents=True,exist_ok=True)
dsn=os.environ.get('TEST_DATABASE_URL') or (root/'.test/database_url').read_text().strip()
if urlparse(dsn).path!='/drama_remediation_test_20261017':sys.exit('Refusing database outside the explicit isolated test DB')
encoder=Path(os.environ.get('DRAMA_TEST_FFMPEG') or shutil.which('ffmpeg') or str(root/'.test/tools/encoder/bin/ffmpeg'))
if not encoder.is_file():sys.exit('Verified isolated H264 encoder required')
env={k:os.environ[k] for k in ('PATH','HOME','LANG','TZ','SSL_CERT_FILE','GOMODCACHE','GOCACHE','GOPATH','GOTOOLCHAIN','TMPDIR') if k in os.environ}
env.update({'PATH':env.get('PATH','/usr/bin:/bin'),'TEST_DATABASE_URL':dsn,'GOCACHE':env.get('GOCACHE',str(root/'.test/gobuild')),'GOPROXY':'off','LIMENG_BASE_URL':'http://127.0.0.1:1','LIMENG_API_KEY':'synthetic-test-only','PUBLIC_BASE_URL':'http://127.0.0.1:1','STORAGE_DIR':str(root/'.test/storage-final-gate'),'DRAMA_TEST_FFMPEG':str(encoder),'FFMPEG_PATH':str(encoder),'FFPROBE_PATH':os.environ.get('FFPROBE_PATH') or shutil.which('ffprobe') or '/usr/bin/ffprobe','CI':'1','NO_COLOR':'1'})
results={}
def run(name,cmd,cwd):
 with (out/(name+'.log')).open('w') as f:r=subprocess.run(cmd,cwd=str(cwd),env=env,stdout=f,stderr=subprocess.STDOUT)
 results[name]={'exit':r.returncode};print(name+' exit='+str(r.returncode),flush=True)
 if r.returncode:save();sys.exit('Gate failed: '+name)
def save():(out/'summary.json').write_text(json.dumps(results,indent=2))
for name,cmd in [('go',['go','test','-json','./...','-count=1','-timeout=180s']),('race',['go','test','-race','-json','./...','-count=1','-timeout=180s'])]:
 run(name,cmd,root/'backend');counts=dict(run=0,pass_count=0,fail=0,skip=0)
 for line in (out/(name+'.log')).read_text().splitlines():
  try:item=json.loads(line)
  except ValueError:continue
  if item.get('Test') and item.get('Action') in ('run','pass','fail','skip'):counts['pass_count' if item['Action']=='pass' else item['Action']]+=1
 results[name].update(counts)
 if counts['run']==0 or counts['run']!=counts['pass_count'] or counts['fail'] or counts['skip']:save();sys.exit('Incomplete test gate: '+name)
run('backend-build',['go','build','-o',str(out/'drama-server'),'./cmd/server'],root/'backend')
run('npm-ci',['npm','ci','--no-audit','--no-fund'],root/'frontend')
run('frontend-tests',['npm','test','--','--reporter=json','--outputFile='+str(out/'vitest.json')],root/'frontend')
v=json.loads((out/'vitest.json').read_text());results['frontend-tests'].update({k:v.get(k) for k in ['numTotalTests','numPassedTests','numFailedTests','numPendingTests']})
if not v.get('numTotalTests') or v.get('numFailedTests') or v.get('numPendingTests') or v.get('numTotalTests')!=v.get('numPassedTests'):save();sys.exit('Incomplete Vitest gate')
node=(out/'frontend-tests.log').read_text()
if not re.search(r'# pass 4\b',node) or not re.search(r'# skipped 0\b',node):save();sys.exit('Original node tests missing or skipped')
results['frontend-tests']['nodeTests']=4
run('typecheck',['npm','run','typecheck'],root/'frontend');run('frontend-build',['npm','run','build'],root/'frontend')
run('nginx-cache',['python3','verification/cache_probe.py'],root)
# Configuration must fail before opening network listeners or database connections.
with (out/'missing-config.log').open('w') as f:p=subprocess.run([str(out/'drama-server'),'api'],env={'PATH':'/usr/bin:/bin'},stdout=f,stderr=subprocess.STDOUT)
results['missing-config']={'exit':p.returncode}
if p.returncode==0 or 'DATABASE_URL' not in (out/'missing-config.log').read_text():save();sys.exit('Startup accepted missing configuration')
save();print('All automated gates passed. Browser and real services remain UNVERIFIED.',flush=True)
