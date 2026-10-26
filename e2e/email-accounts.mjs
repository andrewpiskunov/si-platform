import {chromium} from 'playwright';
import assert from 'node:assert/strict';
import {readdir,readFile,unlink,mkdir} from 'node:fs/promises';
import {resolve} from 'node:path';
import {randomBytes} from 'node:crypto';
const base=process.env.BASE_URL||'http://localhost:5175';
const mailDir=resolve(process.env.MAIL_DIRECTORY||'.local/mail');
const email=`email_${randomBytes(6).toString('hex')}@example.com`;
const password='Example password 123',replacement='Replacement password 456';
const name='Аня · 日本語 · العربية';
const browser=await chromium.launch({executablePath:process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH||undefined});
const context=await browser.newContext();const page=await context.newPage();
page.setDefaultTimeout(10000);const errors=[];page.on('pageerror',e=>errors.push(e.message));
const ownFiles=new Set();let activePassword=password,registered=false;
async function mailLink(mode){
 const deadline=Date.now()+10000;
 while(Date.now()<deadline){
  for(const file of await readdir(mailDir)){
   const path=resolve(mailDir,file);const body=await readFile(path,'utf8');
   if(body.startsWith('To: '+email+'\n')){
    ownFiles.add(path);
    const link=body.match(/https?:\/\/[^\s]+/g)?.find(u=>u.includes('mode='+mode));
    if(link)return link;
   }
  }
  await new Promise(r=>setTimeout(r,50));
 }
 throw new Error('Local email not found');
}
try{
 await page.goto(base+'/login?mode=register');
 await page.getByLabel('Почта',{exact:true}).fill(email);
 await page.getByLabel('Имя в игре',{exact:true}).fill(name);
 await page.getByLabel('Пароль',{exact:true}).fill(password);
 await page.getByLabel('Повторите пароль',{exact:true}).fill('Wrong password 123');
 await page.getByRole('button',{name:'Создать аккаунт',exact:true}).click();
 await page.getByText('Пароли не совпадают. Проверьте повтор пароля.').waitFor();
 await page.getByLabel('Повторите пароль',{exact:true}).fill(password);
 await page.getByRole('button',{name:'Создать аккаунт',exact:true}).click();
 await page.getByRole('status').filter({hasText:'Ссылка действует 24 часа'}).waitFor();
 assert.equal((await context.cookies()).some(c=>c.name==='si_session'),false,'No session before verification');
 const verify=await mailLink('verify');
 await page.goto(verify);
 await page.getByRole('button',{name:'Подтвердить почту',exact:true}).click();
 await page.waitForURL('**/studio');registered=true;
 await page.goto(base+'/account');await page.getByRole('heading',{name,exact:true}).waitFor();
 const me=await context.request.get(base+'/api/account/me');
 assert.equal((await me.json()).user.display_name,name);
 await page.goto(base+'/login?mode=forgot');
 await page.getByLabel('Почта',{exact:true}).fill(email);
 await page.getByRole('button',{name:'Отправить ссылку',exact:true}).click();
 await page.getByRole('status').filter({hasText:'30 минут'}).waitFor();
 await page.goto(await mailLink('reset'));
 await page.getByLabel('Новый пароль',{exact:true}).fill(replacement);
 await page.getByLabel('Повторите пароль',{exact:true}).fill(replacement);
 await page.getByRole('button',{name:'Сохранить пароль',exact:true}).click();
 await page.getByRole('status').filter({hasText:'Пароль изменён'}).waitFor();activePassword=replacement;
 assert.equal((await context.request.get(base+'/api/account/me')).status(),401);
 await page.getByLabel('Почта или логин',{exact:true}).fill(email.toUpperCase());
 await page.getByLabel('Пароль',{exact:true}).fill(replacement);
 await page.getByRole('button',{name:'Войти',exact:true}).click();await page.waitForURL('**/studio');
 assert.equal(errors.length,0,errors.join('\n'));
 // Capture the empty registration form, never passwords or confirmation links.
 await page.goto(base+'/login?mode=register');await page.getByLabel('Почта',{exact:true}).waitFor();
 await mkdir('.local/browser',{recursive:true});await page.screenshot({path:'.local/browser/email-registration.png',animations:'disabled'});
 console.log('PASS: email registration, Unicode name, mismatch, verification, reset, session revocation, email login');
}catch(error){
 console.error('Page alerts:',await page.getByRole('alert').allTextContents());
 console.error('Page heading:',await page.locator('h1').allTextContents());
 throw error;
}finally{
 if(registered)await context.request.delete(base+'/api/account/me',{data:{password:activePassword}});
 for(const path of ownFiles)await unlink(path).catch(()=>{});
 await browser.close();
}
