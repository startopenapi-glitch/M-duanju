const now='2026-10-18T01:00:00Z';
export const user=(id)=>({id,email:id+'@example.com',nickname:'隔离测试'+id,role:id==='admin'?'admin':'user',credits:1000,status:'active',createdAt:now});
const partner={id:'pa',userId:'partner',name:'测试推广伙伴',intro:'全自动短剧创作',wechat:'test-only',code:'TESTCODE',status:'active',version:1};
const invitation={id:'inv-1',email:'invitee@example.com',name:'测试代理',intro:'邮件邀请开通',wechat:'test-only',status:'pending',invitedBy:'admin',expiresAt:now,sendCount:1,lastSentAt:now,createdAt:now};
export const order={id:'order-00001',userId:'ordinary',amountYuan:10,amountCents:1000,credits:1000,status:'paid',channel:'native',outTradeNo:'synthetic-order',credited:true,runId:'11111111111111111111111111111111',createdAt:now};
export const run={id:'11111111111111111111111111111111',projectId:'22222222222222222222222222222222',projectVersion:1,mode:'first',status:'waiting_credits',checkpoint:'shots',autoResume:true,creditsEstimated:2000,requiredCredits:200,creditsSettled:500,requestedAt:now};
const job={id:'job-1',projectId:'22222222222222222222222222222222',projectTitle:'雨停之前：这是用于检验手机换行的长标题',userId:'ordinary',userEmail:'ordinary@example.com',runId:'11111111111111111111111111111111',kind:'shot',status:'reconcile',phase:'submitting',upstreamId:'',errorCode:'submission_unknown',lastError:'提交结果待核对',reservedCredits:200,attempts:0,maxAttempts:2,updatedAt:now};
const audit={id:'audit-1',userId:'admin',action:'credits_adjust',note:'隔离验证记录',createdAt:now};
const credit={id:'credit-1',userId:'ordinary',userEmail:'ordinary@example.com',amount:100,balanceAfter:1000,kind:'recharge',note:'隔离测试',operatorId:'admin',createdAt:now};
const refund={id:'refund-1',orderId:order.id,refundNo:'RF-SYNTHETIC',status:'unknown',amountCents:1000,creditsRevoked:0,creditShortfall:0,reason:'隔离测试',error:'结果未知，请查询',createdAt:now,updatedAt:now};
const pages=(items,u,total=21)=>({items,total,page:Number(u.searchParams.get('page')||1),pageSize:20});

export const project={id:'22222222222222222222222222222222',title:'雨停之后',idea:'两位陌生人在深夜便利店相遇，发现彼此珍藏的是同一个故事。',aspect:'16:9',totalSeconds:15,status:'SHOTS_GENERATING',budgetCredits:2000,spentCredits:500,keyvisualUrl:'',finalUrl:'',errorMsg:'',createdAt:now,updatedAt:now,characters:[],scenes:[],reviews:[],generationJobs:[],shots:[1,2,3].map(i=>({id:'shot-'+i,shotCode:'S0'+i,seq:i,duration:5,status:'QUEUED',videoUrl:'',frames:[],visualPrompt:'便利店暖光与窗外雨幕交织',dialogue:'',retryCount:0}))};

export async function context(browser,base,width,who='admin',options={}){
 const errors=[],unknown=[],outbound=[],requests=[];
 const ctx=await browser.newContext({viewport:{width,height:960},reducedMotion:'reduce'});
 let failNext=false,refundQueries=0,adjustments=[],failAdjustment=false;
 await ctx.addInitScript(({who})=>{if(who)localStorage.setItem('drama_token',who)}, {who});
 await ctx.route('**/*',async route=>{
  const req=route.request(),u=new URL(req.url());
  requests.push(req.method()+' '+u.pathname+u.search);
  if(u.origin!==base){outbound.push(req.url());return route.abort()}
  if(!u.pathname.startsWith('/api/'))return route.continue();
  const path=u.pathname.slice(4),method=req.method(),id=(req.headers().authorization||'').replace('Bearer ','');
  let data,status=200;
  const overridden=options.api?.(path,method,req);
  if(overridden!==undefined){const v=await overridden;return route.fulfill({status:v.status||200,contentType:'application/json',body:JSON.stringify(v.status&&v.status!==200?{ok:false,error:v.error||'隔离失败'}:{ok:true,data:v.data})})}
  if(path==='/auth/me'){if(!id)status=401;else data=user(id)}
  else if(path==='/partner/me')data=id==='partner'?partner:id==='suspended'?{id:'sp',status:'suspended'}:null;
  else if(path==='/projects'&&method==='POST')data=project;
  else if(path==='/projects/creation-context'&&method==='GET')data=project;
  else if(path===`/projects/${project.id}/runs`&&method==='POST')data={run:{...run,status:'queued'}};
  else if(path==='/runs/context')data={run,project:{id:project.id,title:project.title,status:project.status},isLatest:true,hasUnresolvedSubmission:false,availableCredits:1000,shortfallCredits:200};
  else if(path===`/orders/${order.id}/progress`)data={...order,payMode:'wechat',projectId:project.id,paymentEvidence:'recorded',refund:null,observedAt:now};
  else if(path==='/projects' && method==='GET')data=pages([{...project,idea:'长摘要用于检查移动端布局。'.repeat(30)}],u,1);
  else if(path==='/projects/22222222222222222222222222222222')data=project;
  else if(path==='/projects/22222222222222222222222222222222/runs/latest')data={run:{...run,waitingReason:'balance'},availableCredits:1000,shortfallCredits:200};
  else if(path==='/projects/estimate')data={estimatedCredits:630,resolution:'720P',segmentCount:3};
  else if(path==='/auth/totp')data={enabled:false};
  else if(path==='/credits')data={balance:1000,transactions:[credit]};
  else if(path==='/orders')data=pages([order],u,1);
  else if(path==='/pay/status')data={enabled:true};
  else if(path==='/admin/overview')data={users:124,projects:48,completed:21,pendingOrders:3,totalRechargedCents:349000,totalCredits:120000,activeRuns:5,unknownJobs:2,failedRuns:1,refundPending:1,asOf:now};
  else if(path==='/admin/orders'){if(failNext){status=503}else data=pages([order],u)}
  else if(path===`/admin/orders/${order.id}/detail`)data={order,user:user('ordinary'),run,refund,events:[audit]};
  else if(path.endsWith('/refund/query')){refundQueries++;data={...refund,status:'processing'}}
  else if(path==='/admin/credits')data=pages([credit],u);
  else if(path==='/admin/jobs/list')data=pages([job],u);
  else if(path==='/admin/jobs/job-1/detail')data={job,run,events:[audit],shots:[{id:'shot-1',seq:1,status:'QUEUED',upstreamId:'',updatedAt:now}],receipts:[]};
  else if(path==='/admin/users')data=pages([user('ordinary')],u);
  else if(path==='/admin/users/ordinary/detail')data={user:user('ordinary'),version:1,availableCredits:800,reservedCredits:200,projectsCount:1,ordersCount:1,transactions:[credit]};
  else if(path==='/admin/users/credits'&&method==='POST'){adjustments.push(req.postDataJSON());if(failAdjustment){failAdjustment=false;return route.abort('failed')}data={adjusted:true}}
  else if(path==='/admin/partners')data=pages([partner],u);
  else if(path==='/admin/partner-invitations'&&method==='GET')data=pages([invitation],u,1)
  else if(path==='/admin/partner-invitations'&&method==='POST')data=invitation
  else if(path.startsWith('/admin/partner-invitations/'))data=invitation
  else if(path==='/partner/invitations/lookup')data={valid:true,status:'pending',email:'invitee@example.com',name:'测试代理',expiresAt:now}
  else if(path==='/partner/invitations/accept'&&method==='POST')data=partner
  else if(path==='/admin/audit')data=pages([audit],u);
  else if(path==='/admin/system')data={database:'ok',worker:{status:'stale',lastHeartbeat:now},payments:{enabled:true,mock:false},mailConfigured:true,asOf:now};
  else if(path==='/partner/overview')data={newUsers:10,projectUsers:6,firstGenUsers:4,payingUsers:3,dataAsOf:now,commissionOn:false};
  else if(path==='/partner/customers')data=pages([{alias:'客户 8a6f2c',registeredAt:now,hasProject:true,hasGenerated:true,hasPaid:true}],u);
  else if(path==='/partner/orders')data=pages([{maskedId:'订单…1234',amountYuan:'10.00',channel:'native',status:'paid',refundHint:'无退款',createdAt:now}],u);
  else {unknown.push(method+' '+path);status=501}
  return route.fulfill({status,contentType:'application/json',body:JSON.stringify(status===200?{ok:true,data}:{ok:false,error:'隔离测试错误，请重试'})})
 });
 const page=await ctx.newPage();page.on('pageerror',e=>errors.push(String(e)));
 return {ctx,page,errors,unknown,outbound,requests,fail:()=>failNext=true,recover:()=>failNext=false,failAdjustment:()=>failAdjustment=true,adjustments,refundQueries:()=>refundQueries};
}

