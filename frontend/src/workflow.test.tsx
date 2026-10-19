import {CreationProvider} from './features/creation/CreationProvider'
import { expect,test,vi,afterEach } from 'vitest'
import { render,screen,fireEvent,waitFor } from '@testing-library/react'
import { MemoryRouter,Routes,Route } from 'react-router-dom'
import { NewProjectDialog } from './layout/NewProjectDialog'
import { ProjectRail } from './layout/ProjectRail'
import Security from './pages/Security'
import { api,setToken,getToken } from './lib/api'
vi.mock('./App',()=>({useAuth:()=>({user:{id:'u',credits:1000,email:'test@example.com'},setUser:vi.fn()})}))
afterEach(()=>{vi.restoreAllMocks();vi.unstubAllGlobals()})
function wrap(node:React.ReactNode){return render(<MemoryRouter>{node}</MemoryRouter>)}
test('create uses server estimate and authorizes a run without budget input',async()=>{
 vi.spyOn(api,'get').mockResolvedValue({estimatedCredits:855,suggestedBudgetYuan:9,resolution:'720P',segmentCount:3})
 const post=vi.spyOn(api,'post').mockResolvedValue({id:'created'})
 const created=vi.fn()
 render(<MemoryRouter><Routes><Route path="/" element={<CreationProvider><NewProjectDialog onClose={()=>{}} onCreated={created}/></CreationProvider>}/><Route path="/project/:id" element={<p>created project workspace</p>}/></Routes></MemoryRouter>)
 fireEvent.change(screen.getByLabelText('创作想法（一句话即可）'),{target:{value:'隔离测试的合成故事'}})
 await screen.findByText(/855 积分/)
 expect(screen.queryByLabelText(/最高预算/)).toBeNull()
 fireEvent.click(screen.getByRole('button',{name:'开始生成'}))
 await screen.findByText('created project workspace')
 expect(post).toHaveBeenCalledTimes(2)
 expect(post.mock.calls[0][0]).toBe('/projects')
 expect(post.mock.calls[0][1]).not.toHaveProperty('budgetYuan')
 expect(post.mock.calls[1][0]).toBe('/projects/created/runs')
 expect(post.mock.calls[1][1]).toMatchObject({mode:'first'})
 expect(created).toHaveBeenCalledOnce()
})
test('rail loads beyond 20, refreshes and closes navigation',async()=>{
 const items=Array.from({length:20},(_,i)=>({id:`p${i}`,title:`project-${i}`,status:'DRAFT'}))
 const get=vi.spyOn(api,'get').mockImplementation(async(path)=>path.includes('page=2')?{items:[{id:'last',title:'project-21',status:'DRAFT'}],total:21}:{items,total:21})
 const close=vi.fn();wrap(<ProjectRail reloadKey={0} onNew={()=>{}} onNavigate={close}/>)
 await screen.findByText('project-0');fireEvent.click(screen.getByRole('button',{name:'加载更多项目'}));await screen.findByText('project-21')
 fireEvent.click(screen.getByText('project-21'));expect(close).toHaveBeenCalledOnce()
 window.dispatchEvent(new Event('drama:projects-changed'))
 await waitFor(()=>expect(get.mock.calls.length).toBeGreaterThan(2))
})
test('security read failure is explicit instead of implying disabled TOTP',async()=>{
 vi.spyOn(api,'get').mockRejectedValue(new Error('offline'));wrap(<Security/>);
 await screen.findByRole('alert');expect(screen.queryByRole('button',{name:'开启双因素认证'})).toBeNull();expect(screen.getByRole('button',{name:'重试'})).toBeTruthy()
})
test('503 in a project list has retry and never becomes an empty collection',async()=>{
 vi.spyOn(api,'get').mockRejectedValue(new Error('offline'));wrap(<ProjectRail reloadKey={0} onNew={()=>{}} onNavigate={()=>{}}/>);
 expect((await screen.findAllByRole('alert')).some(e=>e.textContent?.includes('加载失败：offline'))).toBe(true);expect(screen.queryByText('还没有项目')).toBeNull();expect(screen.getByRole('button',{name:'重试'})).toBeTruthy()
})
test('recovery codes survive background 401 until explicit acknowledgment',async()=>{
 const {RecoveryNotice}=await import('./lib/recovery')
 vi.spyOn(api,'get').mockResolvedValue({enabled:false})
 const post=vi.spyOn(api,'post').mockImplementation(async(path)=>path.endsWith('/setup')?{secret:'synthetic',qrDataUrl:'data:image/png;base64,AA==',uri:'test'}:{recoveryCodes:['SYNTHETIC1','SYNTHETIC2']})
 setToken('owner-A');wrap(<><Security/><RecoveryNotice/></>)
 fireEvent.click(await screen.findByRole('button',{name:'开启双因素认证'}))
 fireEvent.change(await screen.findByLabelText('输入验证器上显示的 6 位验证码'),{target:{value:'123456'}})
 fireEvent.click(screen.getByRole('button',{name:'确认启用'}));await screen.findByText('SYNTHETIC1')
 post.mockRestore();vi.spyOn(api,'get').mockRestore()
 vi.stubGlobal('fetch',vi.fn(async()=>new Response(JSON.stringify({ok:false,error:'expired'}),{status:401})))
 await expect(api.get('/projects')).rejects.toThrow();expect(getToken()).toBe('owner-A')
 expect(screen.getByText('SYNTHETIC1')).toBeTruthy();expect(JSON.stringify(localStorage)).not.toContain('SYNTHETIC1')
 fireEvent.click(screen.getByRole('button',{name:'我已保存，重新登录'}));expect(getToken()).toBe('');expect(screen.queryByText('SYNTHETIC1')).toBeNull()
})
test('account switch destroys memory-only recovery codes',async()=>{
 const {RecoveryNotice,presentRecovery}=await import('./lib/recovery');const {holdSessionExpiry}=await import('./lib/api');const {act}=await import('@testing-library/react')
 setToken('owner-A');wrap(<RecoveryNotice/>);act(()=>presentRecovery(['SYNTHETIC1'],holdSessionExpiry()));expect(screen.getByText('SYNTHETIC1')).toBeTruthy()
 act(()=>setToken('owner-B'));expect(screen.queryByText('SYNTHETIC1')).toBeNull();expect(getToken()).toBe('owner-B')
})
test('password change explicitly clears the invalidated session',async()=>{
 vi.spyOn(api,'get').mockResolvedValue({enabled:true});vi.spyOn(api,'post').mockResolvedValue({updated:true});setToken('old-session');wrap(<Security/>);
 fireEvent.change(await screen.findByLabelText('当前密码'),{target:{value:'oldpassword'}});fireEvent.change(screen.getByLabelText('新密码（至少 8 位）'),{target:{value:'newpassword'}});fireEvent.change(screen.getByLabelText('确认新密码'),{target:{value:'newpassword'}})
 fireEvent.click(screen.getByRole('button',{name:'修改密码'}));await waitFor(()=>expect(getToken()).toBe(''))
})
test('drawer focus stays inside, Escape closes and returns focus',async()=>{
 const {useDrawer}=await import('./layout/useDrawer')
 vi.stubGlobal('matchMedia',()=>({matches:true,addEventListener:vi.fn(),removeEventListener:vi.fn()}))
 function Drawer(){const d=useDrawer();return <><button onClick={d.openDrawer}>open</button><div ref={d.ref}><button onClick={d.closeDrawer}>close</button><button>last</button></div><p>{d.open?'opened':'closed'}</p></>}
 wrap(<Drawer/>);const open=screen.getByRole('button',{name:'open'});open.focus();fireEvent.click(open);expect(document.body.style.overflow).toBe('hidden');expect(document.activeElement).toBe(screen.getByRole('button',{name:'close'}));fireEvent.keyDown(document,{key:'Tab',shiftKey:true});expect(document.activeElement).toBe(screen.getByRole('button',{name:'last'}));fireEvent.keyDown(document,{key:'Escape'});expect(screen.getByText('closed')).toBeTruthy();expect(document.activeElement).toBe(open);expect(document.body.style.overflow).not.toBe('hidden')
})
