import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import {args,localBase,launch} from './runtime.mjs';
import {context,project,run} from './fixtures.mjs';
const a=args(),base=localBase(a.base||'http://127.0.0.1:4198'),out=a.out||'../../.test/concurrency-review-20261018/browser';
await fs.mkdir(out,{recursive:true});const browser=await launch(),results=[];
try {
 for(const width of [375,768,1440]) for(const scenario of ['legacy-project','known-terminal','durable-replay','legacy-dialog']) {
  const posts=[];const t=await context(browser,base,width,'ordinary',{api:(path,method,req)=>{
   if(path===`/projects/${project.id}/runs/latest`)return {data:null};
   if(path==='/runs/context')return {data:scenario==='known-terminal'?{project,run:{...run,status:'cancelled'}}:null};
   if(path===`/projects/${project.id}/runs`&&method==='POST'){posts.push(req.postDataJSON());return {data:{run:{...run,status:'queued'}}};}
  }});
  try {
   await t.ctx.addInitScript(({id,scenario})=>{
    localStorage.setItem('drama_session_revision','multi-tab-review');
    if(sessionStorage.getItem('review-seeded'))return;
    sessionStorage.setItem('review-seeded','1');
    if(scenario==='legacy-dialog')sessionStorage.setItem('drama_creation_intent',JSON.stringify({account:'ordinary',session:'multi-tab-review',requestKey:'legacy-alias',projectId:id,phase:'start-unknown',error:'旧版提交结果待核对',draft:{idea:'隔离测试故事，不会实际生成',totalSeconds:15,aspect:'16:9',filmReference:'',mood:''}}));
    else sessionStorage.setItem(`drama_project_intent:multi-tab-review:${id}`,JSON.stringify({key:'same-request-key',projectId:id,mode:'first',scope:{totalSeconds:15,aspect:'16:9'},...(scenario==='durable-replay'?{runIdentityVersion:1}:{})}));
   },{id:project.id,scenario});
   const second=await t.ctx.newPage();second.on('pageerror',e=>t.errors.push(String(e)));
   const pages=[t.page,second],path=scenario==='legacy-dialog'?'/':'/project/'+project.id;
   await Promise.all(pages.map(p=>p.goto(base+path)));
   if(scenario==='legacy-dialog') {
    await Promise.all(pages.map(async p=>{await p.getByRole('button',{name:/新建短片/}).last().click();await p.getByRole('button',{name:'核对原请求'}).click();assert.equal(await p.getByRole('button',{name:'恢复原提交'}).count(),0);}));
    await t.page.getByRole('button',{name:'结束本机恢复',exact:true}).click();
    assert.equal(await t.page.evaluate(()=>sessionStorage.getItem('drama_creation_intent')),null);
    assert.ok(await second.evaluate(()=>sessionStorage.getItem('drama_creation_intent')));
   } else {
    await Promise.all(pages.map(p=>p.getByRole('button',{name:'继续自动生成',exact:true}).click()));
    if(scenario==='legacy-project') {
     await Promise.all(pages.map(p=>p.getByRole('alert').filter({hasText:'旧版提交结果无法确认'}).waitFor()));
     await t.page.getByRole('button',{name:'结束本机恢复',exact:true}).click();
     await t.page.getByRole('button',{name:'确认结束本机恢复',exact:true}).click();
     assert.equal(await t.page.evaluate(id=>sessionStorage.getItem(`drama_project_intent:multi-tab-review:${id}`),project.id),null);
     assert.ok(await second.evaluate(id=>sessionStorage.getItem(`drama_project_intent:multi-tab-review:${id}`),project.id));
    } else {
     await Promise.all(pages.map(p=>p.waitForFunction(id=>!sessionStorage.getItem(`drama_project_intent:multi-tab-review:${id}`),project.id)));
    }
   }
   assert.equal(posts.length,scenario==='durable-replay'?2:0);
   if(posts.length) assert.deepEqual(posts.map(x=>x.idempotencyKey),['same-request-key','same-request-key']);
   for(const p of pages)assert.ok(await p.evaluate(()=>document.documentElement.scrollWidth<=document.documentElement.clientWidth+1),'horizontal overflow');
   await second.screenshot({path:`${out}/${scenario}-${width}.png`,fullPage:true});
   assert.deepEqual([...t.errors,...t.unknown,...t.outbound],[]);
   results.push({scenario,width,passed:true,postCount:posts.length});
  }catch(e){results.push({scenario,width,passed:false,error:String(e)});await t.page.screenshot({path:`${out}/failed-${scenario}-${width}.png`,fullPage:true});}
  finally{await t.ctx.close();}
 }
}finally{await browser.close();await fs.writeFile(`${out}/results.json`,JSON.stringify(results,null,2));console.log(JSON.stringify(results,null,2));}
assert.ok(results.every(r=>r.passed));
