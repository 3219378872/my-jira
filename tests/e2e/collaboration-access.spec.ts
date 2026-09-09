import { expect,test } from "@playwright/test";
import { collaborator,login,request,workspace } from "./helpers";

test("REST replacement, reconnect, privacy and membership changes reach live document sessions",async({page,browser})=>{
  test.setTimeout(100_000);
  await login(page);
  const secondContext=await browser.newContext();const second=await secondContext.newPage();
  const member=await collaborator(second);
  expect([201,409]).toContain((await request(page,"POST",`/api/v1/workspaces/${workspace}/members`,{email:member.email,role:15})).status);
  const created=await request(page,"POST",`/api/v1/workspaces/${workspace}/projects`,{name:`Live access ${Date.now()}`,identifier:`L${Date.now().toString(36).toUpperCase()}`,network:"public"});expect(created.status).toBe(201);
  const pid=created.body.data.id;const api=`/api/v1/workspaces/${workspace}/projects/${pid}`;const route=`/w/studio/projects/${pid}`;
  try{
    const membership=await request(page,"POST",`${api}/members`,{user_id:member.id,role:15});expect(membership.status).toBe(201);
    const document=await request(page,"POST",`${api}/pages`,{name:"Access boundary document"});expect(document.status).toBe(201);const id=document.body.data.id;
    await Promise.all([page.goto(`${route}/pages/${id}`),second.goto(`${route}/pages/${id}`)]);
    const firstEditor=page.locator(".document-editor .tiptap"),secondEditor=second.locator(".document-editor .tiptap");
    await expect(firstEditor).toHaveAttribute("contenteditable","true");await expect(secondEditor).toHaveAttribute("contenteditable","true");
    const converted=await request(page,"POST","/live/documents/convert",{document_name:`${workspace}:${pid}:${id}`,html:'<h2>Converted replacement</h2><aside data-callout="warning"><p>Shared content ✨</p></aside>'});expect(converted.status).toBe(200);
    const content=converted.body.data;
    // Raw binary is reserved to the service; ordinary content replacements use HTML + JSON.
    expect((await request(page,"PUT",`${api}/pages/${id}/content`,{...content,version:1})).status).toBe(403);
    delete content.content_binary;
    expect((await request(page,"PUT",`${api}/pages/${id}/content`,{...content,version:1})).status).toBe(200);
    await expect(firstEditor).toContainText("Converted replacement");await expect(secondEditor).toContainText("Shared content ✨");
    await secondContext.setOffline(true);
    await expect(secondEditor).toHaveAttribute("contenteditable","false");
    await firstEditor.click();await page.keyboard.press("Control+End");await page.keyboard.type(" Added while peer was offline.");
    await expect.poll(async()=>(await request(page,"GET",`${api}/pages/${id}/content`)).body.data.content_html).toContain("Added while peer was offline.");
    await secondContext.setOffline(false);
    await expect(secondEditor).toContainText("Added while peer was offline.");
    const now=(await request(page,"GET",`${api}/pages/${id}`)).body.data;
    expect((await request(page,"PATCH",`${api}/pages/${id}`,{is_private:true,version:now.version})).status).toBe(200);
    await expect(secondEditor).toHaveAttribute("contenteditable","false");
    expect((await request(second,"GET",`${api}/pages/${id}/content`)).status).toBe(404);
    const documentName=encodeURIComponent(`${workspace}:${pid}:${id}`);
    expect((await second.request.get(`/live/documents/${documentName}/pdf`)).status()).toBe(403);
    expect((await request(page,"PATCH",`${api}/pages/${id}`,{is_private:false})).status).toBe(200);
    await second.reload();await expect(second.locator(".document-editor .tiptap")).toHaveAttribute("contenteditable","true");
    expect((await request(page,"DELETE",`${api}/members/${membership.body.data.id}`)).status).toBe(204);
    await expect(second.locator(".document-editor .tiptap")).toHaveAttribute("contenteditable","false");
    expect((await request(second,"GET",`${api}/pages/${id}`)).status).toBe(404);
    expect((await request(second,"POST","/live/documents/convert",{document_name:`${workspace}:${pid}:${id}`,html:"<p>Revoked</p>"})).status).toBe(403);
    await expect(firstEditor).toHaveAttribute("contenteditable","true");
  }finally{await secondContext.setOffline(false);await secondContext.close();await request(page,"DELETE",api);}
});
