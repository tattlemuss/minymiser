; -----------------------------------------------------------------------
;	YD (DELTA-PACK) PLAYER CODE
; -----------------------------------------------------------------------
		rsreset
yd_curr		rs.l	1
yd_frame_count	rs.w	1
yd_start	rs.l	1
yd_frames	rs.w	1
yd_size		rs.b	1

; -----------------------------------------------------------------------
; a0 = player state (ds.b yd_size)
; a1 = start of packed ym data
yd_player_init:
	addq.l	#2,a1				; skip header
	move.w	(a1)+,d0			; frame count
	move.l	a1,(a0)+			; curr
	move.w	d0,(a0)+			; frames left
	move.l	a1,(a0)+			; start
	move.w	d0,(a0)+			; frames
	rts

; -----------------------------------------------------------------------
; a0 = player state structure
yd_player_update:
	move.l	yd_curr(a0),a1
	lea	$ffff8800.w,a2
	lea	$ffff8802.w,a3
	subq.w	#1,yd_frame_count(a0)
	bne.s	.no_loop
	move.w	yd_frames(a0),yd_frame_count(a0)
	move.l	yd_start(a0),a1
.no_loop:
r	set	0
	rept	2
	move.b	(a1)+,d0
	rept	7
	add.b	d0,d0
	bcc.s	*+(2+4+2)
	move.b	#r,(a2)
	move.b	(a1)+,(a3)
; bcc jumps to here
r	set	r+1
	endr
	endr
	move.l	a1,yd_curr(a0)
	rts

