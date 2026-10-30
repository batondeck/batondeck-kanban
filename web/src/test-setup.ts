import '@testing-library/jest-dom/vitest';
import { configure } from '@testing-library/react';

// findBy* and waitFor give up after 1 s by default; under load (six busy loops on 4 vCPUs, load 12) a
// drawer's chip took longer than that to render, and the test failed on a correct tree. 5 s raises it
// in step with the 20 s testTimeout in vite.config.ts. A call that passes its own timeout keeps it.
configure({ asyncUtilTimeout: 5_000 });
