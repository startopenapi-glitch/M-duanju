import {test,expect,vi,afterEach} from 'vitest'
import {render,screen,fireEvent,waitFor,act} from '@testing-library/react'
import {MemoryRouter,Routes,Route,Link} from 'react-router-dom'
import ProjectPage from './Project'
import {api,type Project,ApiError} from '../lib/api'
function project(id:string):Project{return{id,title:`project-${id}`,idea:'synthetic',aspect:'16:9',totalSeconds:5,status:'DRAFT',budgetCredits:500,spentCredits:0,keyvisualUrl:'',finalUrl:'',errorMsg:'',createdAt:'',updatedAt:'',characters:[],scenes:[],shots:[],reviews:[]}}
const estimate={estimatedCredits:305,suggestedBudgetYuan:5,resolution:'720P',segmentCount:1}
afterEach(()=>vi.restoreAllMocks())
function renderPage(){return render(<MemoryRouter initialEntries={['/project/a']}><Link to="/project/b">switch</Link><Routes><Route path="/project/:id" element={<ProjectPage/>}/></Routes></MemoryRouter>)}
test('late project response cannot overwrite newly selected project',async()=>{
 let finish!:(p:Project)=>void
 vi.spyOn(api,'get').mockImplementation(async(path)=>path.includes('estimate')?estimate:path==='/projects/a'?new Promise(r=>{finish=r}):project('b'))
 renderPage();fireEvent.click(screen.getByText('switch'));await screen.findByText('project-b');await act(async()=>finish(project('a')));expect(screen.queryByText('project-a')).toBeNull();expect(screen.getByText('project-b')).toBeTruthy()
})
test('legacy project has no manual script entry, only one continue-auto authorization',async()=>{
 vi.spyOn(api,'get').mockImplementation(async p=>p.endsWith('/runs/latest')?null:project('a'))
 const post=vi.spyOn(api,'post').mockResolvedValue({})
 renderPage()
 await screen.findByRole('button',{name:'继续自动生成'})
 expect(screen.queryByRole('button',{name:'生成剧本'})).toBeNull()
 fireEvent.click(screen.getByRole('button',{name:'继续自动生成'}))
 await waitFor(()=>expect(post).toHaveBeenCalledWith('/projects/a/runs',expect.objectContaining({mode:'first'})))
})
test('legacy project offers one explicit continue action, never a budget form',async()=>{
 const p={...project('a'),status:'BUDGET_EXCEEDED',keyvisualUrl:'synthetic.jpg',shots:[{id:'s',shotCode:'S01',seq:1,duration:5,status:'GENERATED',videoUrl:'synthetic.mp4',frames:[],retryCount:0,retryLimit:2,chargeYuan:0,durationActual:5,width:320,height:240}],generationJobs:[{id:'j',kind:'shot',status:'reconcile',phase:'polling',canResume:true,manualRequired:false,errorCode:'settlement_budget',lastError:'预算不足',requiredCredits:600,availableCredits:500,shortfallCredits:100,attempts:0,maxAttempts:2,infrastructureAllowance:4,reservedCredits:275}]}
 vi.spyOn(api,'get').mockImplementation(async path=>{if(path.includes('estimate'))return estimate;if(path.endsWith('/runs/latest'))return null;return p})
 const post=vi.spyOn(api,'post').mockResolvedValue({})
 const view=renderPage();await screen.findByText(/差额 ¥1.00/);expect(view.container.querySelector('video source')?.getAttribute('src')).toBe('synthetic.mp4')
 expect(screen.queryByLabelText(/最高预算/)).toBeNull()
 fireEvent.click(screen.getByRole('button',{name:'继续自动生成'}))
 await waitFor(()=>expect(post).toHaveBeenCalledWith('/projects/a/runs',expect.objectContaining({mode:'first'})))
 expect(post.mock.calls.every(([path])=>path==='/projects/a/runs')).toBe(true)
})

test('waiting run links to credits with the run id',async()=>{
 const p={...project('a'),status:'KEYVISUAL_REVIEW',keyvisualUrl:'synthetic.jpg'}
 const run={id:'run-1',projectId:'a',projectVersion:1,mode:'first',status:'waiting_credits',checkpoint:'',autoResume:true,creditsEstimated:630,creditsSettled:0,requestedAt:''}
 vi.spyOn(api,'get').mockImplementation(async path=>{
   if(path.includes('estimate'))return estimate
   if(path.endsWith('/runs/latest'))return {run,availableCredits:100,shortfallCredits:530}
   return p
 })
 renderPage()
 await screen.findByText(/还差/)
 expect(screen.getByText('530')).toBeTruthy()
 const link=screen.getByRole('link',{name:'充值并继续'})
 expect(link.getAttribute('href')).toBe('/credits?runId=run-1')
 expect(screen.queryByRole('button',{name:/确认定妆照/})).toBeNull()
})
