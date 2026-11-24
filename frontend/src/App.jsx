import { Route, Routes } from 'react-router'
import { useAuth } from './auth/AuthContext.jsx'
import { Layout } from './components/Layout.jsx'
import { RequireAdmin, RequireAuth } from './components/RequireAuth.jsx'
import { Home } from './pages/Home.jsx'
import { Login } from './pages/Login.jsx'
import { NotFound } from './pages/NotFound.jsx'
import { ConnectionForm } from './pages/admin/ConnectionForm.jsx'
import { Connections } from './pages/admin/Connections.jsx'
import { GroupDetail } from './pages/admin/GroupDetail.jsx'
import { Groups } from './pages/admin/Groups.jsx'
import { UserForm } from './pages/admin/UserForm.jsx'
import { Users } from './pages/admin/Users.jsx'
import { DatabaseLayout } from './pages/viewer/DatabaseLayout.jsx'
import { DatabaseOverview } from './pages/viewer/DatabaseOverview.jsx'
import { Databases } from './pages/viewer/Databases.jsx'
import { TableDetail } from './pages/viewer/TableDetail.jsx'
import { RequestDetail } from './pages/workflow/RequestDetail.jsx'
import { Requests } from './pages/workflow/Requests.jsx'

// daftar halaman. key = id user biar state halaman akun lama kebuang waktu ganti akun
export function App() {
  const { user } = useAuth()
  return (
    <Routes key={user?.id ?? 'anon'}>
      <Route path="/login" element={<Login />} />
      <Route element={<RequireAuth />}>
        <Route element={<Layout />}>
          <Route index element={<Home />} />
          <Route path="databases" element={<Databases />} />
          <Route path="databases/:databaseId" element={<DatabaseLayout />}>
            <Route index element={<DatabaseOverview />} />
            <Route path="tables/:schema/:table" element={<TableDetail />} />
            <Route path="requests" element={<Requests />} />
            <Route path="requests/:requestId" element={<RequestDetail />} />
          </Route>
          <Route path="admin" element={<RequireAdmin />}>
            <Route path="users" element={<Users />} />
            <Route path="users/new" element={<UserForm />} />
            <Route path="users/:userId/edit" element={<UserForm />} />
            <Route path="groups" element={<Groups />} />
            <Route path="groups/:groupId" element={<GroupDetail />} />
            <Route path="connections" element={<Connections />} />
            <Route path="connections/new" element={<ConnectionForm />} />
            <Route path="connections/:connectionId/edit" element={<ConnectionForm />} />
          </Route>
          <Route path="*" element={<NotFound />} />
        </Route>
      </Route>
    </Routes>
  )
}
