#!/usr/bin/env python3
"""Dedicated isolated Nginx (native or Docker); never reloads an existing service."""
import os,json,signal,socket,subprocess,threading,tempfile,time,shutil
from pathlib import Path
from http.server import HTTPServer,BaseHTTPRequestHandler
root=Path(__file__).resolve().parents[1];prefix=Path(tempfile.mkdtemp(prefix='drama-nginx-',dir=str(root/'.test')));prefix.chmod(0o755);web=prefix/'web';(web/'assets').mkdir(parents=True)
for name,body in {'index.html':'<html>isolated SPA</html>','assets/index-abcdef12.js':'console.log("synthetic");'*200,'assets/logo.svg':'<svg></svg>','favicon.ico':'test-icon'}.items():
 p=web/name;p.write_text(body);p.chmod(0o644)
def free_port():
 with socket.socket() as sock:sock.bind(('127.0.0.1',0));return sock.getsockname()[1]
port=free_port();nginx=shutil.which('nginx');docker=not nginx;name='drama-cache-'+prefix.name.rsplit('-',1)[-1];api=None;thread=None
config=(root/'ops/nginx-isolated.conf.template').read_text()
if docker:
 # Both servers are synthetic and private to this disposable container. Only the tested proxy is published to loopback.
 config=config.replace('__PREFIX__','/test').replace('__WEB__','/test/web').replace('127.0.0.1:__PORT__','0.0.0.0:8080').replace('__API_PORT__','18082')
 pos=config.rfind('}');config=config[:pos]+' server { listen 127.0.0.1:18082; location / { default_type application/json; return 401 \'{"ok":false}\'; } }\n'+config[pos:]
 cmd=['docker','run','--rm','-v',str(prefix)+':/test','nginx:1.27-alpine','nginx','-c','/test/nginx.conf']
else:
 class Stub(BaseHTTPRequestHandler):
  def do_GET(self):self.send_response(401);self.send_header('Content-Type','application/json');self.end_headers();self.wfile.write(b'{"ok":false}')
  def log_message(self,*args):pass
 api=HTTPServer(('127.0.0.1',0),Stub);thread=threading.Thread(target=api.serve_forever,daemon=True);thread.start()
 config=config.replace('__PREFIX__',str(prefix)).replace('__WEB__',str(web)).replace('__PORT__',str(port)).replace('__API_PORT__',str(api.server_port))
 cmd=[nginx,'-p',str(prefix),'-c',str(prefix/'nginx.conf')]
(prefix/'nginx.conf').write_text(config);results=[];started=False
try:
 with (prefix/'nginx-validation.log').open('w') as f:subprocess.check_call(cmd+['-t'],stdout=f,stderr=subprocess.STDOUT)
 if docker:
  subprocess.check_call(['docker','run','-d','--name',name,'-p',f'127.0.0.1:{port}:8080','-v',str(prefix)+':/test','nginx:1.27-alpine','nginx','-c','/test/nginx.conf','-g','daemon off;'],stdout=subprocess.DEVNULL)
 else:subprocess.check_call(cmd,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
 started=True
 for _ in range(50):
  try:
   with socket.create_connection(('127.0.0.1',port),timeout=.2):break
  except OSError:time.sleep(.1)
 for path,status,cache,gzip in [('/',200,'no-cache',False),('/project/test',200,'no-cache',False),('/assets/index-abcdef12.js',200,'immutable',True),('/assets/logo.svg',200,'no-cache',False),('/favicon.ico',200,'no-cache',False),('/assets/missing-abcdef12.js',404,'no-cache',False),('/api/projects',401,'no-store',False)]:
  r=subprocess.run(['curl','--noproxy','*','--max-time','5','-sS','-o','/dev/null','-D','-','-H','Accept-Encoding: gzip',f'http://127.0.0.1:{port}'+path],capture_output=True)
  text=r.stdout.decode();ok=r.returncode==0 and (' '+str(status)+' ') in text and cache in text and (not gzip or 'content-encoding: gzip' in text.lower());results.append({'path':path,'status':status,'passed':ok,'headers':text})
  if not ok:raise RuntimeError('cache probe failed: '+path)
finally:
 if started:
  if docker:subprocess.run(['docker','rm','-f',name],stdout=subprocess.DEVNULL,check=True)
  else:os.kill(int((prefix/'nginx.pid').read_text()),signal.SIGQUIT)
 if api:api.shutdown();api.server_close();thread.join(timeout=2)
 (root/'.test/cache-probe-last.json').write_text(json.dumps({'evidence':str(prefix),'checks':results},indent=2));(prefix/'results.json').write_text(json.dumps(results,indent=2));print(str(prefix/'results.json'),flush=True)
print('7 isolated nginx cache/gzip checks passed; production untouched.')
