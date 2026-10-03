package fixture

// Handwritten synthetic copies of each statement layout, as pdftotext
// extracts it: the same labels, columns and quirks, with invented people,
// numbers and merchants.

// AxisStatement is an Axis Bank credit card statement for card 4242: an
// opening balance of 500.00, a payment and a refund, a purchase whose
// details wrap onto the lines around it, and charges. 500 - 500 - 100 +
// 1,000 + 0 + 280 = 1,180.00 due.
const AxisStatement = `                                                                                                        SELECT credit card Statement

EXAMPLE PERSON
1 EXAMPLE ROAD,
EXAMPLETOWN 100001

                                                                                              PAYMENT SUMMARY
       Total Payment Due                           Minimum Payment Due                                Statement Period                                Payment Due Date                       Statement Generation Date
            1,180.00 Dr                                      200.00 Dr                           19/09/2026 - 18/10/2026                                   07/11/2026                                        18/10/2026

       Credit Card Number                                  Credit Limit                             Available Credit Limit                           Available Cash Limit
       400000******4242                                     100,000.00                                     98,820.00                                       20,000.00


               Previous Balance - Payments - Credits + Purchase + Cash Advance + Other Debit&Charges =Total Payment Due                                                                        Making only the minimum payment every
                                                                                                                                                                                             month would result in the repayment stretching
          500.00                      500.00               100.00              1,000.00                0.00                         280.00                      1,180.00 Dr                   over years with consequent interest payment

                                                                                                      Account Summary
       DATE                                          TRANSACTION DETAILS                                                                                MERCHANT CATEGORY                                      AMOUNT (Rs.)
                     Card No:         400000******4242                  Name EXAMPLE PERSON
 01/10/2026            BBPS PAYMENT RECEIVED                                                                                                                                                                                  500.00 Cr
                       EXAMPLE OUTDOOR SUPPLIES PRIVATE LIMI
 03/10/2026                                                                                                                         SPORTING GOODS                                                                         1,000.00 Dr
                       TED, PUNE
 06/10/2026            REFUND EXAMPLE CAFE                                                                                          RESTAURANTS                                                                              100.00 Cr
 15/10/2026            LATE PAYMENT FEE                                                                                                                                                                                     237.29 Dr
 15/10/2026            GST                                                                                                                                                                                                   42.71 Dr
                                                                                               **** End of Statement ****

               Your cheque should be payable to Axis Bank Card No.400000******4242 . Please write your NAME & TELEPHONE No. on the reverse of the cheque.

Txn Date     Type                              Cr/Db MAD Contribution          Amount
25th Sep     Purchase                          Db                    2%          5000
`

// ICICIStatement is an ICICI Bank credit card statement for card 4242, as
// layout text (whose transaction amounts are missing, as in real
// extractions) and raw text. 2,000.00 + 1,250.50 + 0.00 - 2,000.00 =
// 1,250.50 due.
var ICICIStatement = struct{ Layout, Raw string }{
	Layout: `CREDIT CARD STATEMENT

MR EXAMPLE PERSON
1 EXAMPLE ROAD

                   STATEMENT DATE                                                 All communications are being sent to your registered e-mail ID and mobile number
             October 12, 2026                                                     l To update email ID and registered mailing address, visit www.icicibank.com > Login

                 PAYMENT DUE DATE                                                 l To update mobile number, visit the nearest ATM or branch
            October 30, 2026
                                                                          STATEMENT SUMMARY


                    Total Amount due                                                 Previous Balance                             Purchases / Charges                         Cash Advances                       Payments / Credits
                                                                      =                                                 +                                         +                                         -
                        ` + "`" + `1,250.50                                                       ` + "`" + `2,000.00                                       ` + "`" + `1,250.50                                    ` + "`" + `0.00                           ` + "`" + `2,000.00

                Minimum Amount due                                        CREDIT SUMMARY
                          ` + "`" + `100.00

                       SPENDS OVERVIEW                                        Date                           SerNo.              Transaction Details                                            Reward           Intl.#        Amount
                                                                              4000XXXXXXXX4242
                                                                              14/09/2026               1000000001                EXAMPLE TELECOM MUMBAI IN                                          5
                                                                              20/09/2026               1000000002                BBPS PAYMENT RECEIVED                                              0                            2,000
                                                                              02/10/2026               1000000003                EXAMPLE BOOKS 24X7 BENGALURU IN                                   15
 Statement period : September 13, 2026 to October 12, 2026
`,
	Raw: `PAYMENT DUE DATE
STATEMENT DATE
` + "`" + `100.00
` + "`" + `1,250.50
October 12, 2026
October 30, 2026
Date SerNo. Transaction Details Reward
Points
Amount (in` + "`" + `)
4000XXXXXXXX4242
14/09/2026 1000000001 EXAMPLE TELECOM MUMBAI IN 5 500.25
20/09/2026 1000000002 BBPS PAYMENT RECEIVED 0 2,000.00 CR
02/10/2026 1000000003 EXAMPLE BOOKS 24X7 BENGALURU IN 15 750.25
Statement period : September 13, 2026 to October 12, 2026
Page 1 of 3
`,
}
