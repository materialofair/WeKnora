const { existsSync } = require('node:fs');
const path = require('node:path');
const { archToString } = require('builder-util');
module.exports = async context => {
  const platform = context.electronPlatformName;
  const directory = path.resolve(__dirname, '../dist/portable', `${platform}-${archToString(context.arch)}`);
  const extension = platform === 'win32' ? '.exe' : '';
  for (const name of [`server${extension}`, `assistant-backup${extension}`, 'config/config.yaml',
    'migrations/sqlite', 'web/index.html', 'jieba/jieba.dict.utf8', 'LICENSE', 'README.md']) {
    if (!existsSync(path.join(directory, name))) throw new Error(`Missing portable resource ${name}; run scripts/package-portable.mjs for ${platform} first.`);
  }
};
