<config name="j"><command name="pull">
	<download over="{{ toJson (list (dict &quot;id&quot; &quot;good&quot; &quot;n&quot; 1) (dict &quot;id&quot; &quot;good&quot; &quot;n&quot; 2) (dict &quot;id&quot; &quot;bad&quot; &quot;n&quot; 1) (dict &quot;id&quot; &quot;bad&quot; &quot;n&quot; 2)) }}"
		group="{{ .id }}" order="{{ .n }}">
		<url>@URL@/part/<value name="id"/>/<value name="n"/></url>
		<to>@PARTS@/<value name="id"/>/<value name="n"/>.part</to>
		<join to="{{ .id }}.out" cleanup="true">@RUN@</join>
	</download>
</command></config>
