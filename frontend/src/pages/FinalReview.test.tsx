import {test,expect,vi,afterEach} from 'vitest'
import {render,screen,fireEvent,waitFor,act} from '@testing-library/react'
import {MemoryRouter,Routes,Route,Link} from 'react-router-dom'
import ProjectPage from './Project'
import {api} from '../lib/api'
afterEach(()=>vi.restoreAllMocks())

function project(id:string,extra:Record<string,unknown>={}){
 return {id,title:'project-'+id,idea:'synthetic',aspect:'16:9',totalSeconds:5,status:'DRAFT',budgetCredits:500,spentCredits:0,keyvisualUrl:'',finalUrl:'',errorMsg:'',createdAt:'',updatedAt:'',characters:[],scenes:[],shots:[],reviews:[],...extra}
}
function failedImage(unknown=false){
 return project('p',{status:'FAILED',errorMsg:'generation failed',generationJobs:[{id:'j',kind:'keyvisual',status:unknown?'reconcile':'failed',phase:unknown?'submitting':'provider_failed',canResume:false,manualRequired:unknown,errorCode:unknown?'submission_unknown':'content_failed',lastError:'failed',requiredCredits:0,availableCredits:0,shortfallCredits:0,attempts:1,maxAttempts:2,infrastructureAllowance:4,reservedCredits:0}]})
}
function renderAt(id:string){
 return render(<MemoryRouter initialEntries={[`/project/${id}`]}><Routes><Route path='/project/:id' element={<ProjectPage/>}/></Routes></MemoryRouter>)
}

test('legacy failed image offers only one continue-auto authorization, never manual retry',async()=>{
 vi.spyOn(api,'get').mockImplementation(async path=>path.endsWith('/runs/latest')?null:failedImage())
 renderAt('p');await screen.findByText('project-p')
 const labels=screen.queryAllByRole('button').map(b=>b.textContent)
 expect(labels.some(s=>/重试.*定妆|重新出图|生成定妆照|重试出图/.test(s||''))).toBe(false)
 expect(labels.some(s=>/继续自动生成/.test(s||''))).toBe(true)
})

test('unknown paid submission is reported without offering a new image attempt',async()=>{
 vi.spyOn(api,'get').mockImplementation(async path=>path.endsWith('/runs/latest')?null:failedImage(true))
 renderAt('p');await screen.findByText('project-p')
 expect(screen.queryByRole('button',{name:'重试定妆照'})).toBeNull()
 expect(screen.getByText(/提交结果未知/)).toBeTruthy()
})

test('switching projects does not retain another project action lock',async()=>{
 const make=(id:string)=>project(id)
 vi.spyOn(api,'get').mockImplementation(async path=>path.endsWith('/runs/latest')?null:make(path.includes('/b')?'b':'a'))
 const post=vi.spyOn(api,'post').mockImplementation(()=>new Promise(()=>{}))
 render(<MemoryRouter initialEntries={['/project/a']}><Link to='/project/b'>switch-project</Link><Routes><Route path='/project/:id' element={<ProjectPage/>}/></Routes></MemoryRouter>)
 fireEvent.click(await screen.findByRole('button',{name:'继续自动生成'}));expect(post).toHaveBeenCalledTimes(1)
 fireEvent.click(screen.getByText('switch-project'));await screen.findByText('project-b')
 const action=screen.getByRole('button',{name:'继续自动生成'});expect(action.hasAttribute('disabled')).toBe(false)
 fireEvent.click(action);expect(post).toHaveBeenCalledTimes(2)
})

test('late action from A cannot release an active B lock',async()=>{
 vi.spyOn(api,'get').mockImplementation(async path=>path.endsWith('/runs/latest')?null:project(path.includes('/b')?'b':'a'))
 let doneA!:(v:object)=>void
 const post=vi.spyOn(api,'post').mockImplementation(path=>path.includes('/a/runs')?new Promise(r=>{doneA=r}):new Promise(()=>{}))
 render(<MemoryRouter initialEntries={['/project/a']}><Link to='/project/b'>switch</Link><Routes><Route path='/project/:id' element={<ProjectPage/>}/></Routes></MemoryRouter>)
 fireEvent.click(await screen.findByRole('button',{name:'继续自动生成'}))
 fireEvent.click(screen.getByText('switch'));await screen.findByText('project-b')
 fireEvent.click(screen.getByRole('button',{name:'继续自动生成'}));expect(post).toHaveBeenCalledTimes(2)
 await act(async()=>doneA({}))
 const action=screen.getByRole('button',{name:/提交中/});expect(action.hasAttribute('disabled')).toBe(true)
 fireEvent.click(action);expect(post).toHaveBeenCalledTimes(2)
})

test('run state error keeps write actions closed and offers retry',async()=>{
 let failed=true
 vi.spyOn(api,'get').mockImplementation(async path=>{
   if(path.endsWith('/runs/latest')&&failed)throw new Error('synthetic unavailable')
   if(path.endsWith('/runs/latest'))return null
   return project('p')
 })
 renderAt('p');await screen.findByText('project-p')
 expect(screen.queryByRole('button',{name:'继续自动生成'})).toBeNull()
 expect(screen.queryByRole('button',{name:'生成剧本'})).toBeNull()
 expect(screen.getByText(/执行状态查询失败/)).toBeTruthy()
 failed=false
 fireEvent.click(screen.getByRole('button',{name:'重试查询'}))
 await screen.findByRole('button',{name:'继续自动生成'})
})
