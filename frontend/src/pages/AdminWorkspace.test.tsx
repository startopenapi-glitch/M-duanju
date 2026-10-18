import { test, expect, vi, afterEach } from 'vitest'
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import AdminOrders from './AdminOrders'
import AdminUsers from './AdminUsers'
import AdminStats from './AdminStats'
import Partner from '../features/partner/PartnerPages'
import { api } from '../lib/api'
const auth = vi.hoisted(() => ({
  user: {
    id: 'admin',
    email: 'admin@example.test',
    role: 'admin',
    credits: 100,
    status: 'active',
  },
  setUser: vi.fn(),
}))
vi.mock('../App', () => ({ useAuth: () => auth }))
afterEach(() => {
  vi.restoreAllMocks()
  sessionStorage.clear()
  auth.user = { ...auth.user, id: 'admin' }
})
const page = (items: unknown[], total = items.length, n = 1) => ({
  items,
  total,
  page: n,
  pageSize: 20,
})
const order = {
  id: 'order-1',
  userId: 'u1',
  amountYuan: '100.00',
  amountCents: 10000,
  credits: 10000,
  status: 'closed',
  channel: 'native',
  credited: false,
  createdAt: '2026-10-18T00:00:00Z',
}
function wrap(node: React.ReactNode) {
  return render(<MemoryRouter>{node}</MemoryRouter>)
}
test('order status is exact and filters are sent to server with paging', async () => {
  const get = vi
    .spyOn(api, 'get')
    .mockImplementation(async (path) =>
      page(
        [{ ...order, id: path.includes('page=2') ? 'page-two' : 'order-1' }],
        21,
        path.includes('page=2') ? 2 : 1,
      ),
    )
  wrap(<AdminOrders />)
  await screen.findByText('已关闭')
  fireEvent.change(screen.getByLabelText('订单状态'), {
    target: { value: 'closed' },
  })
  await waitFor(() =>
    expect(get.mock.calls.some(([p]) => p.includes('status=closed'))).toBe(
      true,
    ),
  )
  fireEvent.click(screen.getByRole('button', { name: '下一页' }))
  await screen.findByText('page-two')
  expect(get.mock.calls.some(([p]) => p.includes('page=2'))).toBe(true)
})
test('admin list error has retry and never becomes empty', async () => {
  let failed = true
  vi.spyOn(api, 'get').mockImplementation(async () => {
    if (failed) throw Error('isolated offline')
    return page([])
  })
  wrap(<AdminOrders />)
  await screen.findByRole('alert')
  expect(screen.queryByText('暂无订单')).toBeNull()
  failed = false
  fireEvent.click(screen.getByRole('button', { name: '重试' }))
  await screen.findByText('暂无订单')
})
test('overview reports recharge in cents and unknown worker does not imply health', async () => {
  vi.spyOn(api, 'get').mockResolvedValue({
    users: 3,
    projects: 2,
    completed: 1,
    pendingOrders: 0,
    totalRechargedCents: 12345,
    totalCredits: 45,
    activeRuns: 1,
    unknownJobs: 2,
    failedRuns: 1,
    refundPending: 2,
    asOf: '2026-10-18T00:00:00Z',
  })
  wrap(<AdminStats />)
  await screen.findByText('¥123.45')
  expect(screen.queryByText(/利润/)).toBeNull()
  expect(screen.getByText('待核对提交')).toBeTruthy()
})
test('order detail distinguishes payment, credited state, run and unknown refund; query does not resubmit', async () => {
  const refund = {
    id: 'r',
    refundNo: 'stable-r',
    status: 'unknown',
    amountCents: 10000,
    creditsRevoked: 0,
    creditShortfall: 0,
  }
  vi.spyOn(api, 'get').mockImplementation(async (path) =>
    path.endsWith('/detail')
      ? {
          order: { ...order, status: 'paid', credited: true },
          user: { id: 'u1', email: 'u@example.test' },
          run: { id: 'run-1', status: 'waiting_credits', checkpoint: 'shot' },
          refund,
          events: [],
        }
      : page([{ ...order, status: 'paid' }]),
  )
  const post = vi
    .spyOn(api, 'post')
    .mockResolvedValue({ ...refund, status: 'processing' })
  wrap(<AdminOrders />)
  fireEvent.click(
    await screen.findByRole('button', { name: '查看订单 order-1' }),
  )
  await screen.findByRole('dialog')
  await screen.findByText('退款结果未知')
  expect(screen.queryByRole('button', { name: '提交退款' })).toBeNull()
  fireEvent.click(screen.getByRole('button', { name: '查询退款结果' }))
  await screen.findByText('退款处理中')
  expect(post).toHaveBeenCalledWith('/admin/orders/order-1/refund/query', {})
})
test('user adjustment retains request id after unknown result and validates integer amount', async () => {
  vi.spyOn(api, 'get').mockImplementation(async (path) =>
    path.endsWith('/detail')
      ? {
          user: {
            id: 'u',
            email: 'u@example.test',
            status: 'active',
            role: 'user',
            credits: 100,
          },
          version: 2,
          availableCredits: 80,
          reservedCredits: 20,
          projectsCount: 1,
          ordersCount: 1,
          transactions: [],
        }
      : page([
          { id: 'u', email: 'u@example.test', status: 'active', credits: 100 },
        ]),
  )
  const post = vi
    .spyOn(api, 'post')
    .mockRejectedValue(Error('network result unknown'))
  wrap(<AdminUsers />)
  fireEvent.click(
    await screen.findByRole('button', { name: '查看用户 u@example.test' }),
  )
  await screen.findByText('可用积分')
  fireEvent.change(screen.getByLabelText('调整积分'), {
    target: { value: '1.5' },
  })
  fireEvent.change(screen.getByLabelText('调整原因'), {
    target: { value: 'isolated adjustment' },
  })
  fireEvent.click(screen.getByRole('button', { name: '确认调整' }))
  expect(post).not.toHaveBeenCalled()
  fireEvent.change(screen.getByLabelText('调整积分'), {
    target: { value: '10' },
  })
  fireEvent.click(screen.getByRole('button', { name: '确认调整' }))
  await screen.findByText(/network result unknown/)
  fireEvent.click(screen.getByRole('button', { name: '重试原调整' }))
  await waitFor(() => expect(post).toHaveBeenCalledTimes(2))
  expect(post.mock.calls[0][1]).toEqual(post.mock.calls[1][1])
  expect(post.mock.calls[0][1]).toHaveProperty('requestId')
})
test('ordinary partner gate never queries private lists', async () => {
  const get = vi
    .spyOn(api, 'get')
    .mockImplementation(async (path) =>
      path === '/partner/me'
        ? null
        : Promise.reject(Error('private endpoint should not run')),
    )
  wrap(<Partner />)
  await screen.findByText('你的账号尚未开通合作伙伴。')
  expect(get).toHaveBeenCalledTimes(1)
})
test('suspended partner gate has no promotion link or save action', async () => {
  const get = vi
    .spyOn(api, 'get')
    .mockImplementation(async (path) =>
      path === '/partner/me'
        ? { id: 's', status: 'suspended' }
        : Promise.reject(Error('private endpoint should not run')),
    )
  wrap(<Partner view="settings" />)
  await screen.findByText('推广合作已暂停')
  expect(screen.queryByRole('button', { name: '保存资料' })).toBeNull()
  expect(get).toHaveBeenCalledTimes(1)
})
test('partner customers fetch current page only and page two replaces page one', async () => {
  vi.spyOn(api, 'get').mockImplementation(async (path) =>
    path === '/partner/me'
      ? { id: 'p', status: 'active', name: 'Partner', code: 'test', version: 1 }
      : path.includes('/customers')
        ? page(
            [
              {
                alias: path.includes('page=2')
                  ? 'customer-two'
                  : 'customer-one',
                registeredAt: '',
              },
            ],
            21,
            path.includes('page=2') ? 2 : 1,
          )
        : Promise.reject(Error('unrequested resource')),
  )
  wrap(<Partner view="customers" />)
  await screen.findByText('customer-one')
  fireEvent.click(screen.getByRole('button', { name: '下一页' }))
  await screen.findByText('customer-two')
  expect(screen.queryByText('customer-one')).toBeNull()
})
test('late admin response is discarded after account switch', async () => {
  const completions: ((value: any) => void)[] = []
  const signals: (AbortSignal | undefined)[] = []
  vi.spyOn(api, 'get').mockImplementation((_path, signal) => {
    signals.push(signal)
    return new Promise((resolve) => {
      completions.push(resolve)
    })
  })
  const view = wrap(<AdminOrders />)
  auth.user = { ...auth.user, id: 'other' }
  view.rerender(
    <MemoryRouter>
      <AdminOrders />
    </MemoryRouter>,
  )
  expect(signals[0]?.aborted).toBe(true)
  await act(async () => completions[1](page([{ ...order, id: 'other-only' }])))
  await screen.findByText('other-only')
  await act(async () =>
    completions[0](page([{ ...order, id: 'private-old-order' }])),
  )
  expect(screen.queryByText('private-old-order')).toBeNull()
  expect(screen.getByText('other-only')).toBeTruthy()
})

test('management shell has six destinations and mobile drawer menu', async () => {
  const { AppShell } = await import('../layout/AppShell')
  vi.spyOn(api, 'get').mockResolvedValue(page([]))
  render(
    <MemoryRouter initialEntries={['/admin/stats']}>
      <AppShell />
    </MemoryRouter>,
  )
  for (const label of [
    '运营概览',
    '创作任务',
    '订单与积分',
    '用户管理',
    '推广合作',
    '系统与审计',
  ])
    expect(screen.getByRole('link', { name: label })).toBeTruthy()
  expect(screen.getByRole('button', { name: '打开菜单' })).toBeTruthy()
})

test('completed job database status is done and does not become unknown', async () => {
  const { default: AdminJobs } = await import('./AdminJobs')
  vi.spyOn(api, 'get').mockResolvedValue(
    page([
      {
        id: 'jdone',
        projectId: 'p',
        projectTitle: 'Finished',
        status: 'done',
        kind: 'shot',
        phase: 'done',
        attempts: 1,
        maxAttempts: 2,
        reservedCredits: 0,
        updatedAt: '',
      },
    ]),
  )
  wrap(<AdminJobs />)
  await screen.findByText('已完成')
  expect(screen.queryByText(/未知状态/)).toBeNull()
  fireEvent.change(screen.getByLabelText('任务状态'), {
    target: { value: 'done' },
  })
  await waitFor(() =>
    expect(screen.getByLabelText('任务状态')).toHaveProperty('value', 'done'),
  )
})
test('unknown job can only submit supplier evidence and never abandon or repeat generation', async () => {
  const { default: AdminJobs } = await import('./AdminJobs')
  const job = {
    id: 'j1',
    projectId: 'p',
    projectTitle: 'Unknown shot',
    status: 'reconcile',
    kind: 'shot',
    phase: 'submitting',
    errorCode: 'submission_unknown',
    attempts: 1,
    maxAttempts: 2,
    reservedCredits: 500,
    updatedAt: '',
  }
  vi.spyOn(api, 'get').mockImplementation(async (p) =>
    p.endsWith('/detail') ? { job, run: null, events: [] } : page([job]),
  )
  const post = vi.spyOn(api, 'post').mockResolvedValue({ reconciled: true })
  wrap(<AdminJobs />)
  fireEvent.click(await screen.findByRole('button', { name: '查看任务 j1' }))
  await screen.findByText('核对未知提交')
  expect(
    screen.queryByRole('button', { name: /放弃|重发|重新生成/ }),
  ).toBeNull()
  const submit = screen.getByRole('button', { name: '提交核对结果' })
  expect(submit.hasAttribute('disabled')).toBe(true)
  fireEvent.change(screen.getByLabelText('已核实的上游任务 ID'), {
    target: { value: 'verified-task' },
  })
  fireEvent.change(screen.getByLabelText('核对依据'), {
    target: { value: 'supplier fact' },
  })
  fireEvent.click(screen.getByRole('checkbox'))
  fireEvent.click(submit)
  await waitFor(() =>
    expect(post).toHaveBeenCalledWith('/admin/jobs/j1/reconcile', {
      project: 'p',
      verifiedTaskId: 'verified-task',
      reason: 'supplier fact',
    }),
  )
})
test('system unknown health is explicit and audit paging is real', async () => {
  const { default: AdminSystem } = await import('./AdminSystem')
  vi.spyOn(api, 'get').mockImplementation(async (p) =>
    p === '/admin/system'
      ? {
          database: 'ok',
          worker: { status: 'unknown', lastHeartbeat: null },
          payments: { enabled: true, mock: false },
          mailConfigured: false,
          asOf: '',
        }
      : page(
          [
            {
              id: 'audit',
              action: p.includes('page=2') ? 'second-audit' : 'first-audit',
              createdAt: '',
            },
          ],
          21,
          p.includes('page=2') ? 2 : 1,
        ),
  )
  wrap(<AdminSystem />)
  await screen.findByText('健康状态未知')
  expect(screen.queryByText('心跳正常')).toBeNull()
  fireEvent.click(screen.getByRole('tab', { name: '审计日志' }))
  await screen.findByText('first-audit')
  fireEvent.click(screen.getByRole('button', { name: '下一页' }))
  await screen.findByText('second-audit')
  expect(screen.queryByText('first-audit')).toBeNull()
})
test('closing detail with Escape returns focus and preserves current list', async () => {
  vi.spyOn(api, 'get').mockImplementation(async (p) =>
    p.endsWith('/detail')
      ? {
          order,
          user: { id: 'u', email: 'u@example.test' },
          run: null,
          refund: null,
          events: [],
        }
      : page([order]),
  )
  wrap(<AdminOrders />)
  const open = await screen.findByRole('button', { name: '查看订单 order-1' })
  open.focus()
  fireEvent.click(open)
  await screen.findByRole('dialog')
  fireEvent.keyDown(document, { key: 'Escape' })
  expect(screen.queryByRole('dialog')).toBeNull()
  expect(document.activeElement).toBe(open)
  expect(screen.getByText('order-1')).toBeTruthy()
})
test('unknown payment status remains unknown rather than pending', async () => {
  vi.spyOn(api, 'get').mockResolvedValue(
    page([{ ...order, status: 'provider_new_state' }]),
  )
  wrap(<AdminOrders />)
  await screen.findByText('未知状态（provider_new_state）')
  expect(
    screen.getByText('未知状态（provider_new_state）').closest('td')
      ?.textContent,
  ).not.toContain('待支付')
})
test('partner identity failure offers retry and never looks like ordinary user', async () => {
  vi.spyOn(api, 'get').mockRejectedValue(Error('identity unavailable'))
  wrap(<Partner />)
  await screen.findByRole('alert')
  expect(screen.queryByText('你的账号尚未开通合作伙伴。')).toBeNull()
  expect(screen.getByRole('button', { name: '重试' })).toBeTruthy()
})

test('mutation error feedback is cleared immediately on identity change', async () => {
  const { useAction } = await import('../features/workspace/resource')
  function Action() {
    const a = useAction()
    return (
      <>
        <button
          onClick={() =>
            a.run(async () => {
              throw Error('private-user-A-error')
            }, 'done')
          }
        >
          act
        </button>
        {a.error && <p>{a.error}</p>}
      </>
    )
  }
  const view = wrap(<Action />)
  fireEvent.click(screen.getByRole('button', { name: 'act' }))
  await screen.findByText('private-user-A-error')
  auth.user = { ...auth.user, id: 'other' }
  view.rerender(
    <MemoryRouter>
      <Action />
    </MemoryRouter>,
  )
  expect(screen.queryByText('private-user-A-error')).toBeNull()
  auth.user = { ...auth.user, id: 'admin' }
})
test('promotion QR is generated locally and has a downloadable artifact', async () => {
  const { PromotionQR } = await import('../features/partner/PromotionQR')
  wrap(<PromotionQR value="https://example.test/p/local-test" />)
  const img = await screen.findByRole('img', { name: '推广链接二维码' })
  expect(img.getAttribute('src')).toMatch(/^data:image/)
  expect(
    screen.getByRole('link', { name: '下载二维码' }).getAttribute('download'),
  ).toBeTruthy()
})

test('uncertain adjustment survives closing and reopening without changing its request id', async () => {
  vi.spyOn(api, 'get').mockImplementation(async (path) =>
    path.endsWith('/detail')
      ? {
          user: {
            id: 'persist-u',
            email: 'persist@example.test',
            status: 'active',
            role: 'user',
            credits: 100,
          },
          version: 2,
          availableCredits: 100,
          reservedCredits: 0,
          projectsCount: 0,
          ordersCount: 0,
          transactions: [],
        }
      : page([
          {
            id: 'persist-u',
            email: 'persist@example.test',
            status: 'active',
            credits: 100,
          },
        ]),
  )
  const post = vi
    .spyOn(api, 'post')
    .mockRejectedValue(Error('unknown adjustment outcome'))
  wrap(<AdminUsers />)
  fireEvent.click(
    await screen.findByRole('button', {
      name: '查看用户 persist@example.test',
    }),
  )
  await screen.findByLabelText('调整积分')
  fireEvent.change(screen.getByLabelText('调整积分'), {
    target: { value: '10' },
  })
  fireEvent.change(screen.getByLabelText('调整原因'), {
    target: { value: 'stable pending adjustment' },
  })
  fireEvent.click(screen.getByRole('button', { name: '确认调整' }))
  await screen.findByText('unknown adjustment outcome')
  fireEvent.click(screen.getByRole('button', { name: '关闭详情' }))
  fireEvent.click(
    screen.getByRole('button', { name: '查看用户 persist@example.test' }),
  )
  await screen.findByRole('button', { name: '重试原调整' })
  expect((screen.getByLabelText('调整积分') as HTMLInputElement).value).toBe(
    '10',
  )
  fireEvent.click(screen.getByRole('button', { name: '重试原调整' }))
  await waitFor(() => expect(post).toHaveBeenCalledTimes(2))
  expect(post.mock.calls[0][1]).toEqual(post.mock.calls[1][1])
})

test('partner profile save displays success and uses returned version for subsequent edits', async () => {
  const me = {
    id: 'p-settings',
    status: 'active',
    name: 'Partner',
    intro: '',
    wechat: '',
    code: 'code',
    version: 1,
  }
  vi.spyOn(api, 'get').mockResolvedValue(me)
  const patch = vi
    .spyOn(api, 'patch')
    .mockImplementation(async (_path, body: any) => ({
      ...me,
      ...body,
      version: body.version + 1,
    }))
  wrap(<Partner view="settings" />)
  fireEvent.change(await screen.findByLabelText('合作方名称'), {
    target: { value: 'Updated Partner' },
  })
  fireEvent.click(screen.getByRole('button', { name: '保存资料' }))
  await screen.findByText('推广资料已保存')
  fireEvent.change(screen.getByLabelText('合作方名称'), {
    target: { value: 'Second edit' },
  })
  fireEvent.click(screen.getByRole('button', { name: '保存资料' }))
  await waitFor(() => expect(patch).toHaveBeenCalledTimes(2))
  expect(patch.mock.calls[1][1]).toMatchObject({ version: 2 })
})
test('historical refund timestamp never renders as a newly confirmed successful refund', async () => {
  vi.spyOn(api, 'get').mockImplementation(async (p) =>
    p.endsWith('/detail')
      ? {
          order: {
            ...order,
            status: 'paid',
            refundedAt: '2026-01-01T00:00:00Z',
          },
          user: { id: 'u', email: 'u@example.test' },
          run: null,
          refund: null,
          events: [],
        }
      : page([{ ...order, status: 'paid' }]),
  )
  wrap(<AdminOrders />)
  fireEvent.click(
    await screen.findByRole('button', { name: '查看订单 order-1' }),
  )
  await screen.findByText(/历史退款记录待渠道核验/)
  expect(screen.queryByRole('button', { name: '提交退款' })).toBeNull()
  expect(screen.queryByText('退款成功')).toBeNull()
})

test('pending generation receipt means awaiting settlement, never awaiting payment', async () => {
  const { default: AdminJobs } = await import('./AdminJobs')
  const job = {
    id: 'receipt-job',
    projectId: 'p',
    projectTitle: 'Receipt',
    status: 'done',
    kind: 'shot',
    phase: 'done',
    attempts: 1,
    maxAttempts: 2,
    reservedCredits: 0,
    updatedAt: '',
  }
  vi.spyOn(api, 'get').mockImplementation(async (p) =>
    p.endsWith('/detail')
      ? {
          job,
          run: null,
          events: [],
          shots: [],
          receipts: [
            {
              id: 'r',
              upstreamId: 'task',
              kind: 'video',
              status: 'pending',
              credits: 500,
              chargeMicros: 500000,
              createdAt: '',
              settledAt: null,
            },
          ],
        }
      : page([job]),
  )
  wrap(<AdminJobs />)
  fireEvent.click(
    await screen.findByRole('button', { name: '查看任务 receipt-job' }),
  )
  await screen.findByText('待结算')
  expect(screen.queryByText('待支付')).toBeNull()
})

test('definitive adjustment validation failure preserves fields but unlocks correction with a new id', async () => {
  const { ApiError } = await import('../lib/api')
  vi.spyOn(api, 'get').mockImplementation(async (path) =>
    path.endsWith('/detail')
      ? {
          user: {
            id: 'correction-u',
            email: 'correction@example.test',
            status: 'active',
            role: 'user',
            credits: 100,
          },
          version: 2,
          availableCredits: 80,
          reservedCredits: 20,
          projectsCount: 0,
          ordersCount: 0,
          transactions: [],
        }
      : page([
          {
            id: 'correction-u',
            email: 'correction@example.test',
            status: 'active',
            credits: 100,
          },
        ]),
  )
  const post = vi
    .spyOn(api, 'post')
    .mockRejectedValueOnce(
      new ApiError('insufficient available balance', { status: 400 }),
    )
    .mockResolvedValue({ adjusted: true })
  wrap(<AdminUsers />)
  fireEvent.click(
    await screen.findByRole('button', {
      name: '查看用户 correction@example.test',
    }),
  )
  await screen.findByLabelText('调整积分')
  fireEvent.change(screen.getByLabelText('调整积分'), {
    target: { value: '-90' },
  })
  fireEvent.change(screen.getByLabelText('调整原因'), {
    target: { value: 'correction reason' },
  })
  fireEvent.click(screen.getByRole('button', { name: '确认调整' }))
  await screen.findByText('insufficient available balance')
  expect((screen.getByLabelText('调整积分') as HTMLInputElement).disabled).toBe(
    false,
  )
  expect((screen.getByLabelText('调整原因') as HTMLInputElement).value).toBe(
    'correction reason',
  )
  fireEvent.change(screen.getByLabelText('调整积分'), {
    target: { value: '-10' },
  })
  fireEvent.click(screen.getByRole('button', { name: '确认调整' }))
  await screen.findByText('积分调整成功')
  const first = post.mock.calls[0][1] as any,
    second = post.mock.calls[1][1] as any
  expect(second.requestId).not.toBe(first.requestId)
  expect(second.amount).toBe(-10)
})

test('paid but uncredited order cannot submit a refund', async () => {
  vi.spyOn(api, 'get').mockImplementation(async (p) =>
    p.endsWith('/detail')
      ? {
          order: { ...order, status: 'paid', credited: false },
          user: { id: 'u', email: 'u@example.test' },
          run: null,
          refund: null,
          events: [],
        }
      : page([{ ...order, status: 'paid' }]),
  )
  wrap(<AdminOrders />)
  fireEvent.click(
    await screen.findByRole('button', { name: '查看订单 order-1' }),
  )
  await screen.findByText(/积分尚未入账/)
  expect(screen.queryByRole('button', { name: '提交退款' })).toBeNull()
})

test('refund validation rejection allows correction, unknown transport outcome remains query only', async () => {
  const { ApiError } = await import('../lib/api')
  vi.spyOn(api, 'get').mockImplementation(async (p) =>
    p.endsWith('/detail')
      ? {
          order: { ...order, status: 'paid', credited: true },
          user: { id: 'u', email: 'u@example.test' },
          run: null,
          refund: null,
          events: [],
        }
      : page([{ ...order, status: 'paid' }]),
  )
  const post = vi
    .spyOn(api, 'post')
    .mockRejectedValueOnce(
      new ApiError('invalid refund reason', { status: 400 }),
    )
    .mockRejectedValueOnce(Error('network unknown'))
  wrap(<AdminOrders />)
  fireEvent.click(
    await screen.findByRole('button', { name: '查看订单 order-1' }),
  )
  fireEvent.change(await screen.findByLabelText('退款原因'), {
    target: { value: 'invalid' },
  })
  fireEvent.click(screen.getByRole('button', { name: '提交退款' }))
  await screen.findByText('invalid refund reason')
  expect((screen.getByLabelText('退款原因') as HTMLInputElement).disabled).toBe(
    false,
  )
  fireEvent.change(screen.getByLabelText('退款原因'), {
    target: { value: 'corrected' },
  })
  fireEvent.click(screen.getByRole('button', { name: '提交退款' }))
  await screen.findByText('network unknown')
  expect(screen.queryByRole('button', { name: '提交退款' })).toBeNull()
  expect(screen.getByRole('button', { name: '查询退款结果' })).toBeTruthy()
  expect(post).toHaveBeenCalledTimes(2)
})
