import { Layout } from 'antd';
import type { ReactNode } from 'react';

export function CenteredScreen({ children }: { children: ReactNode }) {
  return (
    <Layout style={{ alignItems: 'center', display: 'flex', justifyContent: 'center', minHeight: '100vh', padding: 24 }}>
      {children}
    </Layout>
  );
}
