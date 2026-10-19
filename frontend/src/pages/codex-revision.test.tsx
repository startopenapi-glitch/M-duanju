import {afterEach,expect,test,vi} from 'vitest'
import {render,screen,fireEvent,waitFor} from '@testing-library/react'
import {MemoryRouter,Routes,Route,Link} from 'react-router-dom'
import ProjectPage from './Project'
import {api,type Project} from '../lib/api'
afterEach(()=>vi.restoreAllMocks())
const project=(id='a'):Project=>({id,title:'成片-'+id,idea:'synthetic',aspect:'16:9',totalSeconds:10,status:'COMPLETED',budgetCredits:500,spentCredits:40,keyvisualUrl:'local.png',finalUrl:'local.mp4',errorMsg:'',createdAt:'',updatedAt:'',characters:[],scenes:[],shots:[],reviews:[]})
const view={run:{id:'r1',projectId:'a',projectVersion:1,mode:'first',status:'completed',checkpoint:'',autoResume:true,creditsEstimated:200,requiredCredits:0,creditsSettled:40,requestedAt:''},availableCredits:0,shortfallCredits:0}
function mount(){render(<MemoryRouter initialEntries={['/project/a']}><Link to="/project/b">切换项目</Link><Routes><Route path="/project/:id" element={<ProjectPage/>}/></Routes></MemoryRouter>)}
test('revision requires a concrete instruction and one automatic start, no budget confirmation',async()=>{
 vi.spyOn(api,'get').mockImplementation(async path=>path.endsWith('/runs/latest')?view:project())
 const post=vi.spyOn(api,'post').mockResolvedValue({})
 mount();const input=await screen.findByLabelText('修改要求')
 expect(screen.getByRole('button',{name:'生成修改版'}).hasAttribute('disabled')).toBe(true)
 fireEvent.change(input,{target:{value:'结尾改成两个人相视微笑'}})
 fireEvent.click(screen.getByRole('button',{name:'生成修改版'}))
 await waitFor(()=>expect(post).toHaveBeenCalledWith('/projects/a/runs',expect.objectContaining({mode:'revise',scope:expect.objectContaining({instruction:'结尾改成两个人相视微笑'})})))
 expect(screen.queryByText('确认预算')).toBeNull()
})
test('revision draft does not cross projects',async()=>{
 vi.spyOn(api,'get').mockImplementation(async path=>path.endsWith('/runs/latest')?view:project(path.endsWith('/b')?'b':'a'))
 mount();fireEvent.change(await screen.findByLabelText('修改要求'),{target:{value:'a 私有修改'}})
 fireEvent.click(screen.getByText('切换项目'));await screen.findByText('成片-b')
 expect((screen.getByLabelText('修改要求') as HTMLTextAreaElement).value).toBe('')
})
test('pending receipt explains settlement and retains the same recharge run',async()=>{
 vi.spyOn(api,'get').mockImplementation(async path=>path.endsWith('/runs/latest')?{...view,run:{...view.run,status:'waiting_credits',waitingReason:'settlement'},shortfallCredits:120}:project())
 mount();await screen.findByText(/生成结果已保留，等待结算/)
 expect(screen.getByRole('link',{name:'充值并继续'}).getAttribute('href')).toBe('/credits?runId=r1')
 expect(screen.queryByRole('button',{name:'生成修改版'})).toBeNull()
})

test('previous delivery remains downloadable while revising and after completion',async()=>{
 vi.spyOn(api,'get').mockImplementation(async path=>path.endsWith('/runs/latest')?{...view,run:{...view.run,status:'running',projectVersion:2},outputs:[{runId:'v1',projectVersion:1,url:'v1.mp4'}]}:{...project(),status:'SHOTS_REVIEW',finalUrl:'v1.mp4'})
 mount();expect((await screen.findByRole('link',{name:'下载成片'})).getAttribute('href')).toBe('v1.mp4')
})
test('older versions expose labelled links after the latest output replaces the pointer',async()=>{
 vi.spyOn(api,'get').mockImplementation(async path=>path.endsWith('/runs/latest')?{...view,run:{...view.run,projectVersion:2},outputs:[{runId:'v1',projectVersion:1,url:'v1.mp4'},{runId:'r1',projectVersion:2,url:'local.mp4'}]}:project())
 mount();expect((await screen.findByRole('link',{name:'下载 v1'})).getAttribute('href')).toBe('v1.mp4')
})
